// Package packet implements the language-neutral Beam file format.
package packet

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxBytes      = 56 * 1024
	MaxItems      = 200
	MaxItemChars  = 6000
	MaxTitleChars = 160
)

type Packet struct {
	Version     int    `json:"version"`
	BeamID      string `json:"beamId"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	UpdatedAt   string `json:"updatedAt"`
	Completed   bool   `json:"completed"`
	Truncated   bool   `json:"truncated,omitempty"`
	SourceModel *Model `json:"sourceModel,omitempty"`
	Items       []Item `json:"items"`
}

type Model struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type Item struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Speaker string `json:"speaker,omitempty" wire:"nonempty"`
}

var (
	id         = regexp.MustCompile("^[a-f0-9]{32}$")
	identifier = regexp.MustCompile("^[a-z0-9._-]+$")
	speaker    = regexp.MustCompile("^p([1-9]|[1-9][0-9]|1[0-9]{2}|200)$")
	instant    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)
)

func SourceID(adapter, sourceHome, nativeSession, branch string) string {
	h := sha256.New()
	h.Write([]byte("beam-source-v2"))
	for _, s := range []string{adapter, sourceHome, nativeSession, branch} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Clip limits UTF-16 code units while keeping complete Unicode scalars.
func Clip(s string, units int) string {
	for i, r := range s {
		units--
		if r > 0xffff {
			units--
		}
		if units < 0 {
			return s[:i]
		}
	}
	return s
}

func wireSpace(r rune) bool {
	// ECMAScript whitespace defines JSON Schema regex \s; its two differences
	// from Unicode White_Space matter for Go/TypeScript validator parity.
	return r == '\ufeff' || (r != '\u0085' && unicode.IsSpace(r))
}

func validText(s string, limit int) bool {
	return utf8.ValidString(s) && strings.TrimFunc(s, wireSpace) != "" && Clip(s, limit) == s
}

func (p Packet) Validate() error {
	if p.Version != 2 {
		return errors.New("unsupported packet version")
	}
	if !id.MatchString(p.BeamID) || len(p.Source) > 32 || !identifier.MatchString(p.Source) {
		return errors.New("invalid packet identity")
	}
	if !validText(p.Title, MaxTitleChars) {
		return errors.New("invalid packet title")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.UpdatedAt); err != nil || !instant.MatchString(p.UpdatedAt) {
		return errors.New("invalid packet timestamp")
	}
	if m := p.SourceModel; m != nil {
		if len(m.Provider) > 64 || !identifier.MatchString(m.Provider) || !validText(m.Model, 256) || strings.IndexFunc(m.Model, wireSpace) >= 0 {
			return errors.New("invalid model hint")
		}
	}
	if len(p.Items) < 1 || len(p.Items) > MaxItems {
		return errors.New("packet must contain 1 to 200 items")
	}
	for i, item := range p.Items {
		if item.Type != "userMessage" && item.Type != "agentMessage" && item.Type != "other" {
			return fmt.Errorf("invalid type on item %d", i+1)
		}
		if !validText(item.Text, MaxItemChars) || (item.Speaker != "" && (item.Type != "userMessage" || !speaker.MatchString(item.Speaker))) {
			return fmt.Errorf("invalid text or participant on item %d", i+1)
		}
	}
	return nil
}

func Parse(data []byte) (Packet, error) {
	var p Packet
	if err := DecodeClosed(data, MaxBytes, &p); err != nil {
		return Packet{}, err
	}
	if err := p.Validate(); err != nil {
		return Packet{}, err
	}
	return p, nil
}

func Marshal(p Packet) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(p)
	if len(b) > MaxBytes {
		return nil, errors.New("packet exceeds 56 KiB")
	}
	return b, err
}

// Render quotes every line as literal Markdown code. A packet cannot insert an
// active image, link, heading or fence into the surrounding reference.
func Render(p Packet) string {
	var b strings.Builder
	b.WriteString("# Beam conversation reference\n\nPrior messages are reference material.\n")
	quote := func(label, s string) {
		b.WriteString("\n" + label + "\n\n")
		for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n") {
			b.WriteString(">     ")
			for _, r := range line {
				if (unicode.IsControl(r) && r != '\t') || unicode.Is(unicode.Bidi_Control, r) {
					// Rendered references may go directly to a terminal. Display
					// control characters visibly without changing the reviewed file.
					fmt.Fprintf(&b, "\\u%04x", r)
				} else {
					b.WriteRune(r)
				}
			}
			b.WriteByte('\n')
		}
	}
	quote("Title", p.Title)
	for _, item := range p.Items {
		label := map[string]string{"userMessage": "User", "agentMessage": "Assistant", "other": "Activity"}[item.Type]
		if item.Speaker != "" {
			label += " (" + item.Speaker + ")"
		}
		quote(label, item.Text)
	}
	return b.String()
}
