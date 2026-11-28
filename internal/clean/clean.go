// Package clean converts an explicitly selected local projection to a portable
// packet. Origin components and source speaker IDs stay on the source machine.
package clean

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/openclaw/beam/internal/packet"
)

const MaxCandidateBytes = 4 << 20

type Origin struct {
	Adapter       string `json:"adapter"`
	SourceHome    string `json:"sourceHome"`
	NativeSession string `json:"nativeSession"`
}

type Item struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	SpeakerID string `json:"speakerId,omitempty"`
}

type Candidate struct {
	Source       string        `json:"source"`
	Origin       Origin        `json:"origin"`
	Branch       string        `json:"branch,omitempty"`
	Title        string        `json:"title"`
	UpdatedAt    string        `json:"updatedAt"`
	Completed    bool          `json:"completed"`
	SourceModel  *packet.Model `json:"sourceModel,omitempty"`
	Goal         *packet.Goal  `json:"goal,omitempty"`
	Items        []Item        `json:"items"`
	Completeness string        `json:"completeness"`
}

type Options struct {
	OmitItems      []int
	RedactLiterals []string
}

type Report struct {
	Redactions       int  `json:"redactions"`
	Omissions        int  `json:"omissions"`
	LossyConversions int  `json:"lossyConversions"`
	Truncated        bool `json:"truncated"`
}

func ParseCandidate(data []byte) (Candidate, error) {
	var c Candidate
	if err := packet.DecodeClosed(data, MaxCandidateBytes, &c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

func Build(c Candidate, opts Options) (packet.Packet, Report, error) {
	var r Report
	if c.Completeness != "partial" && c.Completeness != "complete" {
		return packet.Packet{}, r, errors.New("invalid completeness")
	}
	if c.Origin.Adapter == "" || c.Source == "" || strings.TrimSpace(c.Origin.SourceHome) == "" || strings.TrimSpace(c.Origin.NativeSession) == "" {
		return packet.Packet{}, r, errors.New("origin requires adapter, source home and native session")
	}
	var mask *regexp.Regexp
	if len(opts.RedactLiterals) > 0 {
		if len(opts.RedactLiterals) > 256 {
			return packet.Packet{}, r, errors.New("too many literal redactions")
		}
		keys := make([]string, 0, len(opts.RedactLiterals))
		for _, s := range opts.RedactLiterals {
			if s == "" {
				return packet.Packet{}, r, errors.New("empty literal redaction")
			}
			keys = append(keys, regexp.QuoteMeta(s))
		}
		// Prefer longer overlapping literals. Replacements never become input to
		// another literal, even when a user chooses a word in the replacement.
		sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		var err error
		mask, err = regexp.Compile(strings.Join(keys, "|"))
		if err != nil {
			return packet.Packet{}, r, errors.New("literal redaction list exceeds the matcher limit")
		}
		mask.Longest()
	}
	omit := make(map[int]bool, len(opts.OmitItems))
	for _, i := range opts.OmitItems {
		if i < 1 || i > len(c.Items) {
			return packet.Packet{}, r, errors.New("omitted item number is outside the selection")
		}
		omit[i] = true
	}
	filter := func(s string) string { return cleanText(s, mask, &r) }
	if filter(c.Source) != c.Source {
		return packet.Packet{}, r, errors.New("source identifier contains private data")
	}
	title := filter(c.Title)
	if title == "" {
		title = "Beamed conversation"
	}
	p := packet.Packet{Version: 2, BeamID: packet.SourceID(c.Origin.Adapter, c.Origin.SourceHome, c.Origin.NativeSession, c.Branch), Source: c.Source, Title: packet.Clip(title, packet.MaxTitleChars), UpdatedAt: c.UpdatedAt, Completed: c.Completed}
	r.Truncated = c.Completeness == "partial" || p.Title != title
	if c.Goal != nil {
		goal := *c.Goal
		goal.Objective = filter(goal.Objective)
		// A clipped goal could authorize different work after resume. Reject it
		// instead of applying transcript-tail truncation to the objective.
		if err := goal.Validate(); err != nil {
			return packet.Packet{}, r, err
		}
		p.Goal, p.Version = &goal, 3
	}
	if m := c.SourceModel; m != nil {
		if filter(m.Provider) == m.Provider && filter(m.Model) == m.Model {
			p.SourceModel = &packet.Model{Provider: m.Provider, Model: m.Model}
		} else {
			r.Omissions++
		}
	}
	// Each source speaker ID stays private until its final packet-local alias
	// is allocated. Oldest items can be dropped without exhausting aliases.
	var speakers []string
	for i, item := range c.Items {
		switch item.Type {
		case "userMessage", "agentMessage", "other":
		default:
			return packet.Packet{}, r, fmt.Errorf("invalid projected item type at %d", i+1)
		}
		if item.SpeakerID != "" && item.Type != "userMessage" {
			return packet.Packet{}, r, errors.New("speaker ID is only valid for a user item")
		}
		if omit[i+1] {
			r.Omissions++
			continue
		}
		text := filter(item.Text)
		clipped := packet.Clip(text, packet.MaxItemChars)
		r.Truncated = r.Truncated || clipped != text
		if packet.IsBlank(clipped) {
			r.Omissions++
			continue
		}
		p.Items = append(p.Items, packet.Item{Type: item.Type, Text: clipped})
		speakers = append(speakers, item.SpeakerID)
	}
	if len(p.Items) == 0 {
		if p.Goal == nil {
			return packet.Packet{}, r, errors.New("no shareable content")
		}
		p.Items = []packet.Item{{Type: "other", Text: "No shareable conversation messages were recorded; the saved goal is included separately."}}
	}
	if n := len(p.Items) - packet.MaxItems; n > 0 {
		p.Items = p.Items[n:]
		speakers = speakers[n:]
		r.Truncated = true
	}
	// Assign packet-local aliases before measuring their encoded cost.
	aliases := map[string]string{}
	for i, s := range speakers {
		if s != "" {
			if aliases[s] == "" {
				aliases[s] = "p" + strconv.Itoa(len(aliases)+1)
			}
			p.Items[i].Speaker = aliases[s]
		}
	}
	p.Truncated = r.Truncated
	if _, err := packet.Marshal(p); err == nil {
		return p, r, nil
	} else if err != packet.ErrTooLarge {
		return packet.Packet{}, r, err
	}
	// Removing an older item only reduces the minimum wire size. Find the
	// longest suffix the packet writer accepts with the truncation flag set.
	p.Truncated = true
	r.Truncated = true
	items := p.Items
	skip := sort.Search(len(items), func(i int) bool {
		p.Items = items[i:]
		_, err := packet.Marshal(p)
		return err != packet.ErrTooLarge
	})
	p.Items = items[skip:]
	if _, err := packet.Marshal(p); err != nil {
		return packet.Packet{}, r, err
	}
	return p, r, nil
}
