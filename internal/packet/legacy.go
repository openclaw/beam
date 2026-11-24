package packet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// The V1 wire format shares data fields with V2 but also carries optional
// transport control. Import discards those fields and never executes a release.
type Legacy struct {
	Version     int          `json:"version"`
	BeamID      string       `json:"beamId"`
	Source      string       `json:"source"`
	Title       string       `json:"title"`
	UpdatedAt   string       `json:"updatedAt"`
	Completed   bool         `json:"completed"`
	Truncated   bool         `json:"truncated,omitempty"`
	HookEvent   string       `json:"hookEvent,omitempty" wire:"nonempty"`
	SourceModel *Model       `json:"sourceModel,omitempty"`
	HandoffID   string       `json:"handoffId,omitempty" wire:"nonempty"`
	Items       []LegacyItem `json:"items"`
}
type LegacyItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func ImportV1(data []byte) (Packet, error) {
	var old Legacy
	if err := DecodeClosed(data, MaxBytes, &old); err != nil {
		return Packet{}, err
	}
	if old.Version != 1 || (old.HookEvent != "" && !validText(old.HookEvent, 64)) || (old.HandoffID != "" && !id.MatchString(old.HandoffID)) {
		return Packet{}, errors.New("invalid v1 upload")
	}
	p := Packet{Version: 2, BeamID: strings.ToLower(strings.TrimSpace(old.BeamID)), Source: strings.ToLower(strings.TrimSpace(old.Source)), Title: strings.TrimSpace(old.Title), UpdatedAt: strings.TrimSpace(old.UpdatedAt), Completed: old.Completed, Truncated: old.Truncated, SourceModel: old.SourceModel}
	if p.SourceModel != nil {
		p.SourceModel.Provider = strings.ToLower(strings.TrimSpace(p.SourceModel.Provider))
		p.SourceModel.Model = strings.TrimSpace(p.SourceModel.Model)
	}
	for _, i := range old.Items {
		p.Items = append(p.Items, Item{Type: i.Type, Text: strings.TrimSpace(i.Text)})
	}
	if err := p.Validate(); err != nil {
		return Packet{}, err
	}
	return p, nil
}

// V1Projection makes a separate v1 upload for explicit review. Packet-local
// participant aliases cannot be represented. Never overwrite a reviewed file.
func V1Projection(p Packet) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	old := Legacy{Version: 1, BeamID: p.BeamID, Source: p.Source, Title: p.Title, UpdatedAt: p.UpdatedAt, Completed: p.Completed, Truncated: p.Truncated, SourceModel: p.SourceModel}
	for _, i := range p.Items {
		old.Items = append(old.Items, LegacyItem{Type: i.Type, Text: i.Text})
	}
	b, err := json.Marshal(old)
	if len(b) > MaxBytes {
		return nil, errors.New("v1 projection exceeds 56 KiB")
	}
	return b, err
}

// LegacySourceID preserves links produced by the released agent-skills CLI.
func LegacySourceID(source, identity string) string {
	h := sha256.Sum256([]byte(source + ":" + identity))
	return hex.EncodeToString(h[:16])
}

// LegacyMirrorID preserves snapshots from an existing OpenClaw catalog mirror.
func LegacyMirrorID(catalog, host, thread string) string {
	h := sha256.Sum256([]byte(catalog + "\x00" + host + "\x00" + thread))
	return hex.EncodeToString(h[:16])
}
