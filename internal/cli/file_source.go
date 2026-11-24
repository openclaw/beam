package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/openclaw/beam/internal/adapters/file"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

// Repacking an existing portable selection must retain its source identity. If
// cleaning leaves a v2 document semantically unchanged, its reviewed bytes survive too.
func repack(data []byte, opts clean.Options) (packet.Packet, []byte, clean.Report, error) {
	p, err := file.ReadPacket(data)
	if err != nil {
		return packet.Packet{}, nil, clean.Report{}, err
	}
	c := clean.Candidate{Source: p.Source, Origin: clean.Origin{Adapter: p.Source, SourceHome: "portable", NativeSession: p.BeamID}, Title: p.Title, UpdatedAt: p.UpdatedAt, Completed: p.Completed, SourceModel: p.SourceModel, Completeness: "complete"}
	if p.Truncated {
		c.Completeness = "partial"
	}
	for _, i := range p.Items {
		c.Items = append(c.Items, clean.Item{Type: i.Type, Text: i.Text, SpeakerID: i.Speaker})
	}
	cleaned, report, err := clean.Build(c, opts)
	if err != nil {
		return cleaned, nil, report, err
	}
	cleaned.BeamID = p.BeamID
	if _, err := packet.Parse(data); err == nil && reflect.DeepEqual(cleaned, p) {
		return cleaned, data, report, nil
	}
	wire, err := packet.Marshal(cleaned)
	return cleaned, wire, report, err
}

func stdinSelection(data []byte, opts clean.Options) (packet.Packet, []byte, clean.Report, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return repack(data, opts)
	}
	if !utf8.Valid(data) {
		return packet.Packet{}, nil, clean.Report{}, fmt.Errorf("stdin requires a Beam packet or UTF-8 text")
	}
	c := clean.Candidate{Source: "file", Origin: clean.Origin{Adapter: "file", SourceHome: "stdin", NativeSession: packet.Digest(data)}, Title: "Selected text", UpdatedAt: "1970-01-01T00:00:00Z", Completeness: "complete", Completed: true, Items: []clean.Item{{Type: "other", Text: string(data)}}}
	p, report, err := clean.Build(c, opts)
	if err != nil {
		return p, nil, report, err
	}
	wire, err := packet.Marshal(p)
	return p, wire, report, err
}
