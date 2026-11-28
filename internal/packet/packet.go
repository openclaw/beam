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
	MaxBytes       = 56 * 1024
	MaxItems       = 200
	MaxItemChars   = 6000
	MaxTitleChars  = 160
	MaxGoalChars   = 4000
	MaxSafeInteger = int64(9007199254740991)
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
	Goal        *Goal  `json:"goal,omitempty"`
	Items       []Item `json:"items"`
}

// Goal is portable task intent. Native goal identity and usage accounting stay
// with the source; tokensUsed only determines the remaining copied budget.
type Goal struct {
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget,omitempty"`
	TokensUsed  int64  `json:"tokensUsed"`
}

func (g Goal) Validate() error {
	if !utf8.ValidString(g.Objective) || IsBlank(g.Objective) || utf8.RuneCountInString(g.Objective) > MaxGoalChars {
		return errors.New("goal objective must contain 1 to 4000 Unicode characters")
	}
	switch g.Status {
	case "active", "paused", "blocked", "usageLimited", "budgetLimited", "complete":
	default:
		return errors.New("invalid goal status")
	}
	if g.TokensUsed < 0 || g.TokensUsed > MaxSafeInteger || g.TokenBudget != nil && (*g.TokenBudget < 1 || *g.TokenBudget > MaxSafeInteger) {
		return errors.New("goal token counters must be safe nonnegative integers and budget must be positive")
	}
	return nil
}

// ReferenceText describes source state without authorizing a destination run.
func (g Goal) ReferenceText() string {
	text := fmt.Sprintf("/goal %s\nSource status: %s\nTokens used at source: %d", g.Objective, g.Status, g.TokensUsed)
	if g.TokenBudget != nil {
		text += fmt.Sprintf("\nSource token budget: %d", *g.TokenBudget)
	}
	return text
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

// IsBlank follows the packet schema's ECMAScript whitespace definition.
func IsBlank(s string) bool {
	return strings.TrimFunc(s, wireSpace) == ""
}

func validText(s string, limit int) bool {
	return utf8.ValidString(s) && !IsBlank(s) && Clip(s, limit) == s
}

func (p Packet) Validate() error {
	if p.Version != 2 && p.Version != 3 {
		return errors.New("unsupported packet version")
	}
	if p.Goal != nil {
		if p.Version != 3 {
			return errors.New("goals require packet version 3")
		}
		if err := p.Goal.Validate(); err != nil {
			return err
		}
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

// Go escapes these characters for HTML and JavaScript embedding. Standalone
// JSON permits their UTF-8 spellings. Consume escaped backslashes in pairs
// so literal text such as \\u0026 is never interpreted as an escape.
var compactEscapes = strings.NewReplacer(
	`\\`, `\\`,
	`\u0026`, "&", `\u003c`, "<", `\u003e`, ">",
	`\u2028`, "\u2028", `\u2029`, "\u2029",
)

// ErrTooLarge means even the compact wire encoding exceeds MaxBytes.
var ErrTooLarge = errors.New("packet exceeds 56 KiB")

func Marshal(p Packet) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return marshalBounded(p, ErrTooLarge)
}

func marshalBounded(value any, sizeError error) ([]byte, error) {
	b, err := json.Marshal(value)
	if len(b) > MaxBytes {
		// Keep every previously valid encoding byte-identical for reviewed digests.
		b = []byte(compactEscapes.Replace(string(b)))
		if len(b) > MaxBytes {
			return nil, sizeError
		}
	}
	return b, err
}

// Render quotes every line as literal Markdown code. A packet cannot insert an
// active image, link, heading or fence into the surrounding reference.
func Render(p Packet) string {
	var b strings.Builder
	size := len(p.Title)
	for _, item := range p.Items {
		size += len(item.Text)
	}
	b.Grow(size)
	b.WriteString("# Beam conversation reference\n\nPrior messages are reference material.\n")
	quote := func(label, speaker, s string) {
		b.WriteByte('\n')
		b.WriteString(label)
		if speaker != "" {
			b.WriteString(" (")
			b.WriteString(speaker)
			b.WriteByte(')')
		}
		b.WriteString("\n\n>     ")
		for {
			i := strings.IndexFunc(s, referenceEscape)
			if i < 0 {
				b.WriteString(s)
				break
			}
			b.WriteString(s[:i])
			r, width := utf8.DecodeRuneInString(s[i:])
			s = s[i+width:]
			switch r {
			case '\r', '\n':
				if r == '\r' {
					s = strings.TrimPrefix(s, "\n")
				}
				b.WriteString("\n>     ")
			case utf8.RuneError:
				b.WriteRune(r)
			default:
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		}
		b.WriteByte('\n')
	}
	quote("Title", "", p.Title)
	if g := p.Goal; g != nil {
		quote("Historical goal (reference only)", "", g.ReferenceText())
	}
	for _, item := range p.Items {
		label := map[string]string{"userMessage": "User", "agentMessage": "Assistant", "other": "Activity"}[item.Type]
		quote(label, item.Speaker, item.Text)
	}
	return b.String()
}

// Printable spans can go straight to the builder. Decode RuneError separately
// so an invalid source byte still renders as the visible replacement character.
func referenceEscape(r rune) bool {
	if r < utf8.RuneSelf {
		return r < ' ' && r != '\t' || r == 0x7f
	}
	return r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}
