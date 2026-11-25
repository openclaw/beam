package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

// Admission is shared with delivery. Opening probes again because a plan is
// informational and confers no permission or reservation at the destination.
func destinationOperation(c adapter.Capabilities, mode string) (string, error) {
	var op string
	switch mode {
	case "", "auto":
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
		return "", fmt.Errorf("--mode must be auto, native, stage, or manual")
	}
	if !slices.Contains(c.Destination, op) {
		return "", adapter.Unsupported(op)
	}
	return op, nil
}

type visibility struct {
	History      string `json:"history"`
	ModelContext string `json:"modelContext"`
}

func plan(ctx context.Context, dst adapter.Adapter, o options, raw []byte, out io.Writer) (any, error) {
	c, err := caps(ctx, dst)
	if err != nil {
		return nil, err
	}
	op, err := destinationOperation(c, o.values["mode"])
	if err != nil {
		return nil, err
	}
	id := o.values["in"]
	view := visibility{"unknown", "unknown"}
	// Only the selected built-in has this version-specific evidence. Third-party
	// adapters can choose their own reported name, including that of a built-in.
	if _, external := dst.(adapter.External); !external {
		switch id + "@" + c.Version {
		case "file@1":
			view = visibility{"manual-markdown", "requires-manual-attachment"}
		case "codex@0.172.0":
			view = visibility{"no-prior-ui-turns", "reviewed-reference"}
		case "opencode@1.19.6":
			view.History = "one-native-reference-message"
		case "claude@2.1.296 (Claude Code)", "claude@2.1.296", "pi@0.83.0":
			view = visibility{"staged-reference", "reference-path-on-explicit-turn"}
		case "gemini@0.45.0":
			view.History = "reference-notice-on-import"
		}
	}
	canSend := slices.Contains(c.Destination, "send")
	if o.values["json"] == "" {
		action := "Stage a reference"
		if op == "openCopy" {
			action = "Open a native copy"
		}
		_, err := fmt.Fprintf(out, "%s\n%s in %s (%q). Opening starts zero model turns.\nHistory: %s\nModel context: %s\nExplicit send supported: %t\nRecovery: %q\n", packet.Digest(raw), action, id, c.Version, strings.ReplaceAll(view.History, "-", " "), strings.ReplaceAll(view.ModelContext, "-", " "), canSend, c.Recovery)
		return nil, err
	}
	return map[string]any{
		"planVersion": 1, "destination": id, "adapterVersion": c.Version,
		"digest": packet.Digest(raw), "operation": op, "visibility": view,
		"startsTurn": false, "supportsExplicitSend": canSend, "recovery": c.Recovery,
	}, nil
}
