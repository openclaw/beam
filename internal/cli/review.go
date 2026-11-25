package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

// Visible numbers belong to this input digest. Selection never reopens the
// source or reapplies cleaning, which could move an item to a different number.
func review(p packet.Packet, raw []byte, o options, out io.Writer) (any, error) {
	digest := packet.Digest(raw)
	if len(o.keep) == 0 && len(o.clean.OmitItems) == 0 {
		if o.values["out"] != "" {
			return nil, fmt.Errorf("choose --keep or --omit to write an edited packet")
		}
		if o.values["json"] != "" {
			return nil, writeReviewJSON(out, raw, map[string]any{"digest": digest, "bytes": len(raw)})
		}
		if _, err := fmt.Fprintf(out, "%s  %d bytes\n%q  source=%q  id=%q  updated=%s  completed=%t  truncated=%t\n", digest, len(raw), p.Title, p.Source, p.BeamID, p.UpdatedAt, p.Completed, p.Truncated); err != nil {
			return nil, err
		}
		if p.SourceModel != nil {
			if _, err := fmt.Fprintf(out, "model=%q/%q\n", p.SourceModel.Provider, p.SourceModel.Model); err != nil {
				return nil, err
			}
		}
		for i, item := range p.Items {
			if _, err := fmt.Fprintf(out, "%3d  %s %s  %q\n", i+1, item.Type, item.Speaker, item.Text); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if err := o.require("expect-digest", "out"); err != nil {
		return nil, err
	}
	if len(o.keep) > 0 && len(o.clean.OmitItems) > 0 {
		return nil, fmt.Errorf("choose either --keep or --omit")
	}
	numbers := o.clean.OmitItems
	keeping := len(o.keep) > 0
	if keeping {
		numbers = o.keep
	}
	selected := make([]bool, len(p.Items))
	for _, n := range numbers {
		if n > len(selected) {
			return nil, fmt.Errorf("item %d is outside the reviewed range 1..%d", n, len(selected))
		}
		selected[n-1] = true
	}
	originalCount := len(p.Items)
	items := p.Items[:0]
	for i, item := range p.Items {
		if selected[i] == keeping {
			items = append(items, item)
		}
	}
	p.Items = items
	if len(items) == 0 {
		return nil, fmt.Errorf("keep at least one reviewed item")
	}
	edited := raw
	if len(items) != originalCount {
		var err error
		edited, err = packet.Marshal(p)
		if err != nil {
			return nil, err
		}
	}
	path := o.values["out"]
	editedDigest := packet.Digest(edited)
	if path == "-" && o.values["json"] != "" {
		return nil, writeReviewJSON(out, edited, map[string]any{"sourceDigest": digest, "digest": editedDigest, "items": len(items)})
	}
	if err := save(path, edited, out); err != nil {
		return nil, err
	}
	if path == "-" {
		return nil, nil
	}
	return map[string]any{"sourceDigest": digest, "digest": editedDigest, "items": len(items), "path": path}, nil
}

// Keep the validated packet literal intact inside the JSON envelope. Feeding
// RawMessage back through Marshal changes whitespace and HTML-sensitive text,
// invalidating the digest and sometimes expanding a packet past its byte limit.
func writeReviewJSON(out io.Writer, raw []byte, fields map[string]any) error {
	header, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "{\"protocol\":%d,\"ok\":true,\"result\":%s,\"packet\":%s}}\n", adapter.Protocol, header[:len(header)-1], raw)
	return err
}
