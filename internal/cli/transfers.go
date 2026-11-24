package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/file"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

func selected(ctx context.Context, a adapter.Adapter, selection string, opts clean.Options) (packet.Packet, clean.Report, error) {
	var source clean.Candidate
	var report clean.Report
	seen := map[string]bool{}
	cursor := ""
	budget := 0
	for {
		page, err := call[adapter.ReadResult](ctx, a, adapter.Request{Op: "read", Selection: selection, Cursor: cursor, Limit: 50})
		if err != nil {
			return packet.Packet{}, report, err
		}
		data, err := json.Marshal(page)
		if err != nil {
			return packet.Packet{}, report, err
		}
		budget += len(data)
		if budget > clean.MaxCandidateBytes {
			return packet.Packet{}, report, adapter.Fail("too_large", "never", "selected history exceeds 4 MiB; select a narrower source")
		}
		if cursor == "" {
			source = page.Candidate
		} else {
			if page.Source != source.Source || page.Origin != source.Origin || page.Branch != source.Branch || page.UpdatedAt != source.UpdatedAt {
				return packet.Packet{}, report, adapter.Fail("source_changed", "never", "source changed during pagination; select it again")
			}
			source.Items = append(source.Items, page.Items...)
			if page.Completeness == "partial" {
				source.Completeness = "partial"
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if seen[cursor] || len(seen) >= 4096 {
			return packet.Packet{}, report, fmt.Errorf("adapter did not finish paging selected source")
		}
		seen[cursor] = true
	}
	return clean.Build(source, opts)
}

func loadInput(name string, in io.Reader, max int64) ([]byte, error) {
	if name == "-" {
		return adapter.ReadBounded(in, max)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return adapter.ReadBounded(f, max)
}

func save(name string, data []byte, out io.Writer) error {
	if name == "" || name == "-" {
		_, err := out.Write(data)
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".beam-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func pick(v string) string {
	if v != "" {
		return v
	}
	return id()
}

func inputPacket(name string, in io.Reader) (packet.Packet, []byte, error) {
	raw, err := loadInput(name, in, packet.MaxBytes)
	if err != nil {
		return packet.Packet{}, nil, err
	}
	p, err := file.ReadPacket(raw)
	return p, raw, err
}

func matchDigest(expected string, data []byte) error {
	if expected != "" && expected != packet.Digest(data) {
		return adapter.Fail("revision_changed", "never", "reviewed digest differs from the current packet")
	}
	return nil
}

func sourceSelection(o options) (string, error) {
	if err := o.require("from", "session"); err != nil {
		return "", err
	}
	s := o.values["session"]
	if b := o.values["branch"]; b != "" {
		if strings.Contains(s, "#") {
			return "", fmt.Errorf("session already contains a branch selector")
		}
		s += "#" + b
	}
	return s, nil
}

func (a App) pack(ctx context.Context, reg Registry, o options) (packet.Packet, []byte, clean.Report, error) {
	var report clean.Report
	s, err := sourceSelection(o)
	if err != nil {
		return packet.Packet{}, nil, report, err
	}
	src, err := a.selectAdapter(reg, o.values["from"])
	if err != nil {
		return packet.Packet{}, nil, report, err
	}
	p, report, err := selected(ctx, src, s, o.clean)
	if err != nil {
		return p, nil, report, err
	}
	data, err := packet.Marshal(p)
	return p, data, report, err
}

type delivery struct {
	Reference adapter.OpenResult `json:"reference"`
	Send      adapter.SendResult `json:"send"`
}

func open(ctx context.Context, dst adapter.Adapter, p packet.Packet, o options) (any, error) {
	c, err := caps(ctx, dst)
	if err != nil {
		return nil, err
	}
	mode := o.values["mode"]
	if mode == "" {
		mode = "auto"
	}
	var op string
	switch mode {
	case "auto":
		if slices.Contains(c.Destination, "openCopy") {
			op = "openCopy"
		} else {
			op = "stageReference"
		}
	case "native":
		op = "openCopy"
	case "stage", "manual":
		op = "stageReference"
	default:
		return nil, fmt.Errorf("--mode must be auto, native, stage, or manual")
	}
	if !slices.Contains(c.Destination, op) {
		return nil, adapter.Unsupported(op)
	}
	message := o.values["message"]
	if message != "" && !slices.Contains(c.Destination, "send") {
		return nil, adapter.Unsupported("send")
	}
	operationID := pick(o.values["operation-id"])
	res, err := call[adapter.OpenResult](ctx, dst, adapter.Request{Op: op, Packet: p, OperationID: operationID})
	if err != nil {
		return nil, err
	}
	switch res.State {
	case "ready":
		if res.Handle == "" || res.NativeSession == "" {
			return nil, fmt.Errorf("adapter returned ready without a native session and handle")
		}
	case "staged":
		if res.Handle == "" || res.NextAction == "" {
			return nil, fmt.Errorf("adapter returned a staged reference without a handle and next action")
		}
	case "manual":
		if res.Artifact == "" || res.NextAction == "" {
			return nil, fmt.Errorf("adapter returned manual delivery without an artifact and next action")
		}
	case "needs_reconciliation":
		return nil, adapter.Fail("needs_reconciliation", "after_reconcile", fmt.Sprintf("reconcile operation %s: %s", operationID, res.NextAction))
	default:
		return nil, fmt.Errorf("adapter returned unknown delivery state")
	}
	if mode == "native" && res.State != "ready" {
		return nil, adapter.Fail("needs_reconciliation", "after_reconcile", "adapter did not verify the requested native copy")
	}
	res.OperationID = operationID
	if message == "" {
		return res, nil
	}
	sent, err := call[adapter.SendResult](ctx, dst, adapter.Request{Op: "send", RequestID: pick(o.values["request-id"]), Handle: res.Handle, Instruction: message})
	if err != nil {
		return nil, err
	}
	return delivery{Reference: res, Send: sent}, nil
}
