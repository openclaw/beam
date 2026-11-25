package receiver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openclaw/beam/internal/bundle"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

type commandOptions struct {
	endpoint, state, profile, out, id, message, agent, requestID string
	json, login                                                  bool
	arg                                                          string
}

func parseOptions(args []string) (o commandOptions, err error) {
	selected := map[string]bool{}
	positionals := false
	for i := 0; i < len(args); i++ {
		value := args[i]
		if value == "--" && !positionals {
			positionals = true
			continue
		}
		if !strings.HasPrefix(value, "--") || positionals {
			if o.arg != "" {
				return o, failure("one_argument_required", "never")
			}
			o.arg = value
			continue
		}
		key, v, has := strings.Cut(value, "=")
		if selected[key] {
			return o, failure("duplicate_option", "never")
		}
		selected[key] = true
		if key == "--json" || key == "--cloudflare-login" {
			if has {
				return o, failure("invalid_option", "never")
			}
			if key == "--json" {
				o.json = true
			} else {
				o.login = true
			}
			continue
		}
		if !has {
			i++
			if i == len(args) {
				return o, failure("missing_option_value", "never")
			}
			v = args[i]
		}
		switch key {
		case "--endpoint", "--to":
			if o.endpoint != "" {
				return o, failure("duplicate_endpoint", "never")
			}
			o.endpoint = v
		case "--state":
			o.state = v
		case "--out":
			o.out = v
		case "--beam-id":
			o.id = v
		case "--message":
			o.message = v
		case "--agent":
			o.agent = v
		case "--request-id":
			o.requestID = v
		case "--profile-dir":
			o.profile = v
		default:
			return o, failure("unknown_option", "never")
		}
	}
	return o, nil
}

var nameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// A name maps only to an operator-configured origin. Never derive the credential target from an input link.
func resolveEndpoint(name string) (string, error) {
	if name == "" {
		name = os.Getenv("BEAM_ENDPOINT")
	}
	if strings.Contains(name, "://") {
		return name, nil
	}
	if !nameRE.MatchString(name) {
		return "", failure("receiver_endpoint_required", "never")
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(dir, "beam", "receivers.json"))
	if err != nil {
		return "", failure("unknown_receiver_name", "never")
	}
	defer f.Close()
	var names map[string]string
	if json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&names) != nil || names[name] == "" {
		return "", failure("unknown_receiver_name", "never")
	}
	return names[name], nil
}
func packetInput(name string, stdin io.Reader, allowBundle bool) ([]byte, error) {
	if name == "" {
		return nil, failure("snapshot_file_required", "never")
	}
	r := stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	reader := bufio.NewReader(r)
	limit := packet.MaxBytes
	if magic, _ := reader.Peek(4); allowBundle && string(magic) == "PK\x03\x04" {
		limit = bundle.MaxArchiveBytes
	}
	b, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, failure("too_large", "never")
	}
	return b, nil
}
func outputFile(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	f, err := os.CreateTemp(dir, ".beam-pull-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, dst)
}
func commandExit(err error) int {
	var e *Error
	if errors.Is(err, journal.ErrConflict) {
		return 5
	}
	if !errors.As(err, &e) {
		return 2
	}
	if e.HTTP == 401 || e.HTTP == 403 || e.Code == "publisher_cannot_release" || strings.HasPrefix(e.Code, "cloudflare_") {
		return 4
	}
	if e.HTTP == 409 || e.HTTP == 410 || e.HTTP == 412 || e.Retry == "after_reconcile" {
		return 5
	}
	if e.Retry == "same_request" {
		return 6
	}
	if e.HTTP == 404 || strings.Contains(e.Code, "unsupported") || strings.Contains(e.Code, "unavailable") {
		return 3
	}
	return 2
}

// Run implements the remote CLI verbs. All credential selection belongs to the explicitly configured origin.
func Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	code, err := run(ctx, name, args, stdin, stdout)
	if err != nil && slices.Contains(args, "--json") {
		machineCode, retry := "invalid_request", "never"
		var typed *Error
		if errors.As(err, &typed) {
			machineCode, retry = typed.Code, typed.Retry
		}
		jsonErr := json.NewEncoder(stdout).Encode(map[string]any{"protocol": 1, "ok": false, "error": map[string]string{"code": machineCode, "retry": retry, "message": err.Error()}})
		return code, jsonErr
	}
	return code, err
}

func run(ctx context.Context, name string, args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	o, err := parseOptions(args)
	if err != nil {
		return 2, err
	}
	endpoint, err := resolveEndpoint(o.endpoint)
	if err != nil {
		return commandExit(err), err
	}
	headers, err := EnvHeaders()
	if err != nil {
		return commandExit(err), err
	}
	c, err := New(Config{Endpoint: endpoint, StatePath: o.state, Headers: headers, CloudflareLogin: o.login})
	if err != nil {
		return commandExit(err), err
	}
	defer c.Close()
	var result any
	var raw []byte
	switch name {
	case "share", "handoff":
		if o.arg != "" || name == "share" {
			raw, err = packetInput(o.arg, stdin, name == "share")
			if err != nil {
				break
			}
		}
		if name == "share" && len(raw) >= 4 && string(raw[:4]) == "PK\x03\x04" {
			result, err = c.shareScreenshot(ctx, raw, o.id)
			break
		}
		var meta struct {
			Version int    `json:"version"`
			ID      string `json:"beamId"`
		}
		if raw != nil {
			if json.Unmarshal(raw, &meta) != nil || !beamIDRE.MatchString(meta.ID) {
				err = failure("invalid_packet", "never")
				break
			}
			if o.id != "" && o.id != meta.ID {
				err = failure("beam_id_mismatch", "never")
				break
			}
			o.id = meta.ID
		}
		if c.legacy {
			if raw != nil && meta.Version != 1 {
				err = failure("review_v1_projection_first", "never")
				break
			}
			if name == "share" {
				result, err = c.ShareV1(ctx, raw)
				break
			}
			var found bool
			_, found, err = c.LegacyRow(ctx, o.id)
			if err != nil {
				break
			}
			if !found {
				err = failure("legacy_handoff_unsupported", "never")
				break
			}
			result, err = c.HandoffV1(ctx, o.id, raw, false)
			break
		}
		if name == "share" {
			result, err = c.Share(ctx, raw)
		} else {
			result, err = c.Handoff(ctx, o.id, raw)
		}
	case "pull", "resolve", "continue":
		if o.arg == "" {
			err = failure("publication_link_required", "never")
			break
		}
		link := o.arg
		var head Publication
		if _, machineErr := c.publicationID(link); machineErr != nil {
			head, err = c.resolveHuman(ctx, link, o.profile)
			link = head.APIURL
			if err == nil && head.State == "legacy_unbound" && name != "pull" {
				err = failure("unbound_snapshot", "never")
			}
			if err != nil {
				break
			}
		}
		switch name {
		case "pull":
			if head.State == "legacy_unbound" {
				raw, err = c.pullSnapshot(ctx, v2Path+"/snapshots/"+head.BeamID+"/snapshot", head)
			} else {
				raw, err = c.Pull(ctx, link)
			}
			if err != nil {
				break
			}
			if o.out == "" || o.out == "-" {
				_, err = stdout.Write(raw)
				if err != nil {
					return 2, err
				}
				return 0, nil
			}
			if err = outputFile(o.out, raw); err == nil {
				result = map[string]any{"path": o.out, "digest": packet.Digest(raw)}
			}
		case "resolve":
			result, err = c.Resolve(ctx, link)
		case "continue":
			result, err = c.Continue(ctx, link, Instruction{RequestID: o.requestID, Text: o.message, Agent: o.agent})
		}
	default:
		err = failure("unknown_remote_command", "never")
	}
	if err != nil {
		return commandExit(err), err
	}
	if o.json {
		err = json.NewEncoder(stdout).Encode(map[string]any{"protocol": 1, "ok": true, "result": result})
	} else {
		switch r := result.(type) {
		case Publication:
			_, err = fmt.Fprintf(stdout, "Snapshot shared\n%s\n%s\n", r.URL, r.APIURL)
		case Release:
			_, err = fmt.Fprintf(stdout, "Handoff released\n%s\n%s\n", r.URL, r.APIURL)
		default:
			err = json.NewEncoder(stdout).Encode(result)
		}
	}
	if err != nil {
		return 2, err
	}
	return 0, nil
}
