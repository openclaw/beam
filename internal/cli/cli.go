package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/cleanstdio"
	"github.com/openclaw/beam/internal/packet"
)

const usage = `Usage: beam COMMAND [options]

  beam capabilities [--json]                 Show CLI, packet, and adapter versions
  beam doctor [--json]                       Probe available harness integrations
  beam list --from HARNESS                   List local, selectable conversations
  beam pack --from HARNESS --session ID      Write a reviewed packet (--out FILE or stdout)
  beam clean --protocol 1                    Clean one selected history projection from stdin
  beam bundle create PACKET --attach FILE --out ZIP     Add files to a portable bundle
                       [--strip-png-metadata]           Re-encode static PNGs without metadata
  beam bundle inspect ZIP                                Verify contents and show file digests
  beam bundle preview ZIP --out HTML                     Write an offline preview
  beam inspect FILE                          Show exact packet and its digest
  beam review FILE                           Number visible items; edit with --keep N or --omit N
  beam convert FILE --packet-version 1       Prepare and preview an explicit v1 projection
  beam render FILE [--out FILE]              Render a packet as quoted Markdown
  beam plan FILE --in HARNESS                 Preview destination behavior without opening or sending
  beam open FILE --in HARNESS                Open an independent copy or reviewed reference
  beam push --from A --session ID --to B     Copy one selected conversation to another harness
  beam resolve --in HARNESS --handle ID      Resolve an existing local reference
  beam lookup --in HARNESS --operation-id ID Query a previous operation
  beam send --in HARNESS --handle ID --message TEXT
  beam share FILE --to ENDPOINT              Publish a reviewed packet or single-PNG bundle
  beam handoff FILE --to ENDPOINT            Release a reviewed packet
  beam pull LINK --out FILE                  Read an authorized remote snapshot
  beam continue LINK --message TEXT          Send a new instruction at the destination
  beam adapter test --in HARNESS             Verify protocol capabilities and optional fixture operations
  beam adapter --in HARNESS                  Serve one protocol-1 request on stdin

Use - for stdin or stdout. Use --json for machine receipts, --dry-run for a push
preview, and --expect-digest SHA to send reviewed bytes. Use --adapter ID=/path
to invoke an explicitly selected executable implementing the Beam stdio protocol.
`

func (a App) Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(out, a.Version)
		return 0
	}
	cmd, argv := args[0], args[1:]
	if cmd == "bundle" && len(argv) > 0 {
		cmd, argv = "bundle "+argv[0], argv[1:]
	}
	if cmd == "adapter" && len(argv) > 0 && argv[0] == "test" {
		cmd, argv = "adapter test", argv[1:]
	}
	jsonMode := cmd == "adapter" || slices.Contains(argv, "--json")
	fail := func(err error) int {
		if jsonMode {
			_ = json.NewEncoder(out).Encode(adapter.Failure(err))
		} else {
			fmt.Fprintln(stderr, "beam:", err)
		}
		return adapter.ExitCode(err)
	}
	switch cmd {
	case "skill-v1":
		if a.Legacy != nil {
			return a.Legacy(ctx, argv, in, out, stderr)
		}
	case "hook-notice":
		if a.Hook != nil {
			if len(argv) != 2 {
				return fail(fmt.Errorf("hook-notice requires adapter and reference handle"))
			}
			if err := a.Hook(out, argv[0], argv[1]); err != nil {
				return fail(err)
			}
			return 0
		}
	case "share", "pull", "handoff", "continue", "resolve":
		local := cmd == "resolve" && (slices.Contains(argv, "--in") || hasOption(argv, "--in="))
		if !local {
			if a.Remote == nil {
				return fail(adapter.Unsupported(cmd))
			}
			code, err := a.Remote(ctx, cmd, argv, in, out, stderr)
			if err != nil {
				fmt.Fprintln(stderr, "beam:", err)
				if code == 0 {
					code = adapter.ExitCode(err)
				}
			}
			return code
		}
	}
	if _, ok := optionsFor[cmd]; !ok {
		return fail(fmt.Errorf("unknown command %q; use beam help", cmd))
	}
	o, err := parseOptions(cmd, argv)
	if err != nil {
		return fail(err)
	}
	reg, err := a.registry(o)
	if err != nil {
		return fail(err)
	}
	if cmd == "adapter" {
		if err := o.require("in"); err != nil {
			return fail(err)
		}
		chosen, err := a.selectAdapter(reg, o.values["in"])
		if err != nil {
			return fail(err)
		}
		code, err := adapter.Serve(ctx, chosen, in, out)
		if err != nil {
			return fail(err)
		}
		return code
	}
	result, err := a.execute(ctx, cmd, o, reg, in, out, stderr)
	if err != nil {
		return fail(err)
	}
	if result != nil {
		if jsonMode {
			env, err := adapter.Success(result)
			if err != nil {
				return fail(err)
			}
			err = json.NewEncoder(out).Encode(env)
			if err != nil {
				return fail(err)
			}
		} else {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return fail(err)
			}
		}
	}
	return 0
}

func hasOption(args []string, prefix string) bool {
	for _, s := range args {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (a App) execute(ctx context.Context, cmd string, o options, reg Registry, in io.Reader, out, stderr io.Writer) (any, error) {
	withFile := cmd == "bundle create" || cmd == "bundle inspect" || cmd == "bundle preview" || cmd == "plan" || cmd == "review" || cmd == "inspect" || cmd == "render" || cmd == "open" || cmd == "convert"
	if withFile && len(o.args) != 1 {
		return nil, fmt.Errorf("%s requires exactly one file or -", cmd)
	}
	if !withFile && len(o.args) != 0 {
		return nil, fmt.Errorf("unexpected positional argument for %s", cmd)
	}
	switch cmd {
	case "bundle create", "bundle inspect", "bundle preview":
		return bundleCommand(cmd, o, in, out)
	case "adapter test":
		if err := o.require("in"); err != nil {
			return nil, err
		}
		dst, err := a.selectAdapter(reg, o.values["in"])
		if err != nil {
			return nil, err
		}
		return testAdapter(ctx, dst, o, in)
	case "capabilities":
		info := map[string]any{
			"version": a.Version, "cliProtocol": 1, "adapterProtocol": 1, "adapters": keys(reg),
			"packetVersions": []int{1, 2}, "bundleVersions": []int{1},
			"bundleTransforms": []string{"strip-png-metadata"},
		}
		if a.Legacy != nil {
			info["skillProtocols"] = []int{1}
		}
		return info, nil
	case "doctor":
		type detected struct {
			ExecutableAvailable bool                  `json:"executableAvailable"`
			ID                  string                `json:"id"`
			Ready               bool                  `json:"available"`
			Capabilities        *adapter.Capabilities `json:"capabilities,omitempty"`
			Error               *adapter.Error        `json:"error,omitempty"`
		}
		ids := keys(reg)
		rows := make([]detected, len(ids))
		done := make(chan int, len(ids))
		for i, id := range ids {
			go func(i int, id string) {
				probe, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				c, err := caps(probe, reg[id])
				rows[i] = detected{ID: id, Ready: err == nil, ExecutableAvailable: err == nil && c.Version != "unavailable"}
				if err == nil {
					rows[i].Capabilities = &c
				} else {
					rows[i].Error = adapter.Describe(err)
				}
				done <- i
			}(i, id)
		}
		for range ids {
			<-done
		}
		return map[string]any{"version": a.Version, "adapters": rows}, nil
	case "list":
		if err := o.require("from"); err != nil {
			return nil, err
		}
		src, err := a.selectAdapter(reg, o.values["from"])
		if err != nil {
			return nil, err
		}
		limit := 50
		if s := o.values["limit"]; s != "" {
			limit, err = strconv.Atoi(s)
			if err != nil || limit < 1 || limit > 200 {
				return nil, fmt.Errorf("--limit must be between 1 and 200")
			}
		}
		return call[adapter.ListResult](ctx, src, adapter.Request{Op: "list", Cursor: o.values["cursor"], Limit: limit})
	case "clean":
		return nil, cleanstdio.Run(o.values["protocol"], in, out, stderr, o.clean)
	case "pack", "push":
		p, b, report, err := a.pack(ctx, reg, o, in)
		if err != nil {
			return nil, err
		}
		if err := matchDigest(o.values["expect-digest"], b); err != nil {
			return nil, err
		}
		if cmd == "pack" || o.values["dry-run"] != "" {
			if o.values["json"] != "" {
				result := map[string]any{"digest": packet.Digest(b), "bytes": len(b), "report": report}
				if dst := o.values["out"]; dst != "" && dst != "-" {
					if err := save(dst, b, out); err != nil {
						return nil, err
					}
					result["path"] = dst
					return result, nil
				}
				return nil, writePacketReceipt(out, b, result)
			}
			fmt.Fprintf(stderr, "%s  %d bytes\n", packet.Digest(b), len(b))
			return nil, save(o.values["out"], b, out)
		}
		if err := o.require("to"); err != nil {
			return nil, err
		}
		dst, err := a.selectAdapter(reg, o.values["to"])
		if err != nil {
			return nil, err
		}
		return open(ctx, dst, p, o)
	case "inspect", "review", "render", "plan", "open", "convert":
		p, b, err := inputPacket(o.args[0], in)
		if err != nil {
			return nil, err
		}
		if err := matchDigest(o.values["expect-digest"], b); err != nil {
			return nil, err
		}
		if cmd == "review" {
			return review(p, b, o, out)
		}
		if cmd == "convert" {
			var outBytes []byte
			switch o.values["packet-version"] {
			case "1":
				outBytes, err = packet.V1Projection(p)
			case "2":
				outBytes, err = packet.Marshal(p)
			default:
				return nil, fmt.Errorf("choose --packet-version 1 or 2")
			}
			if err != nil {
				return nil, err
			}
			if o.values["packet-version"] == "1" {
				for _, item := range p.Items {
					if item.Speaker != "" {
						fmt.Fprintln(stderr, "v1 omits historical speaker aliases")
						break
					}
				}
			}
			fmt.Fprintln(stderr, packet.Digest(outBytes))
			if o.values["json"] != "" {
				if name := o.values["out"]; name != "" && name != "-" {
					if err := save(name, outBytes, out); err != nil {
						return nil, err
					}
					return map[string]any{"path": name, "digest": packet.Digest(outBytes)}, nil
				}
				return nil, writePacketReceipt(out, outBytes, map[string]any{"digest": packet.Digest(outBytes)})
			}
			return nil, save(o.values["out"], outBytes, out)
		}
		if cmd == "inspect" {
			if o.values["json"] != "" {
				return nil, writePacketReceipt(out, b, map[string]any{"digest": packet.Digest(b), "bytes": len(b)})
			}
			fmt.Fprintf(stderr, "%s  %d bytes\n", packet.Digest(b), len(b))
			return nil, save("-", b, out)
		}
		if cmd == "render" {
			return nil, save(o.values["out"], []byte(packet.Render(p)), out)
		}
		if err := o.require("in"); err != nil {
			return nil, err
		}
		dst, err := a.selectAdapter(reg, o.values["in"])
		if err != nil {
			return nil, err
		}
		if cmd == "plan" {
			return plan(ctx, dst, o, b, out)
		}
		return open(ctx, dst, p, o)
	case "send", "resolve", "lookup":
		if err := o.require("in"); err != nil {
			return nil, err
		}
		dst, err := a.selectAdapter(reg, o.values["in"])
		if err != nil {
			return nil, err
		}
		if cmd == "lookup" {
			if err := o.require("operation-id"); err != nil {
				return nil, err
			}
			return lookupLocal(ctx, bindingKey(o.values["in"], dst), dst, o.values["operation-id"], o.values["state"])
		}
		if err := o.require("handle"); err != nil {
			return nil, err
		}
		if cmd == "resolve" {
			return call[adapter.OpenResult](ctx, dst, adapter.Request{Op: "resolve", Handle: o.values["handle"]})
		}
		if err := o.require("message"); err != nil {
			return nil, err
		}
		c, err := caps(ctx, dst)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(c.Destination, "send") {
			return nil, adapter.Unsupported("send")
		}
		return recordCall[adapter.SendResult](ctx, bindingKey(o.values["in"], dst), dst, adapter.Request{Op: "send", Handle: o.values["handle"], Instruction: o.values["message"], RequestID: pick(o.values["request-id"])}, c.Recovery, o.values["state"])
	}
	return nil, adapter.Unsupported(cmd)
}
