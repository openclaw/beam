package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

type V1Receipt struct {
	OK               bool       `json:"ok"`
	BeamID           string     `json:"beamId"`
	URL              string     `json:"url"`
	PublicationID    string     `json:"publicationId,omitempty"`
	CanView          *bool      `json:"canView,omitempty"`
	SnapshotFallback bool       `json:"snapshotFallback,omitempty"`
	Handoff          *V1Handoff `json:"handoff,omitempty"`
}
type V1Handoff struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Access string `json:"access"`
}

var prettyURL = regexp.MustCompile(`^(?:([a-z0-9]+(?:-[a-z0-9]+)*)-)?([a-f0-9]{12,32})$`)
var agentName = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,63}$`)

func (c *Client) checkV1Receipt(b []byte, id string) (V1Receipt, error) {
	var r V1Receipt
	var fields map[string]json.RawMessage
	if len(b) == 0 {
		b = []byte("{}")
	}
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return r, failure("invalid_receipt_json", "after_reconcile")
	}
	if v, exists := fields["canView"]; exists && string(v) != "true" && string(v) != "false" {
		return r, failure("invalid_can_view", "after_reconcile")
	}
	if json.Unmarshal(b, &r) != nil {
		return r, failure("invalid_receipt", "after_reconcile")
	}
	if _, exists := fields["handoff"]; exists && (r.Handoff == nil || r.BeamID != id || r.URL == "" || !beamIDRE.MatchString(r.Handoff.ID) || r.Handoff.Owner != "gateway" || r.Handoff.Access != "owned" && r.Handoff.Access != "shared") {
		return r, failure("invalid_handoff_receipt", "after_reconcile")
	}
	r.BeamID = id
	if _, exists := fields["ok"]; !exists {
		r.OK = true
	}
	if r.URL == "" {
		return r, nil
	}
	if strings.IndexFunc(r.URL, func(v rune) bool {
		return v <= 32 || v >= 127 && v <= 159 || v >= 0x200b && v <= 0x200d || v == 0x2060 || v == 0xfeff
	}) >= 0 {
		return r, failure("invalid_receiver_link", "after_reconcile")
	}
	u, err := trustedLink(c.base, r.URL, true)
	if err != nil {
		return r, err
	}
	rel := strings.TrimPrefix(u.Path, c.base.Path+"/")
	if name, ok := strings.CutPrefix(rel, "beam/"); ok {
		m := prettyURL.FindStringSubmatch(name)
		if len(m) != 3 || len(m[1]) > 48 || !strings.HasPrefix(id, m[2]) || u.RawQuery != "" {
			return r, failure("invalid_receiver_link", "after_reconcile")
		}
	} else if agent, ok := strings.CutPrefix(rel, "chat/"); ok {
		q := u.Query()
		if !agentName.MatchString(agent) || len(q) != 3 || len(q["host"]) != 1 || len(q["catalog"]) != 1 || len(q["thread"]) != 1 || q.Get("catalog") != "beam" || q.Get("host") != "gateway" || q.Get("thread") != id {
			return r, failure("invalid_receiver_link", "after_reconcile")
		}
	} else {
		return r, failure("invalid_receiver_link", "after_reconcile")
	}
	r.URL = u.String()
	return r, nil
}

func (c *Client) LegacyRow(ctx context.Context, id string) (journal.LegacyRow, bool, error) {
	s, err := c.journal()
	if err != nil {
		return journal.LegacyRow{}, false, err
	}
	return s.Legacy(ctx, c.legacyEndpoint, id)
}
func (c *Client) SaveLegacyDestination(ctx context.Context, id, key, sessionID string) error {
	s, err := c.journal()
	if err != nil {
		return err
	}
	return s.SaveLegacyDestination(ctx, c.legacyEndpoint, id, key, sessionID)
}

func legacyPacket(data []byte) (string, error) {
	p, err := packet.ImportV1(data)
	if err != nil {
		return "", err
	}
	var ctl struct {
		HandoffID json.RawMessage `json:"handoffId"`
	}
	if json.Unmarshal(data, &ctl) != nil || ctl.HandoffID != nil {
		return "", failure("file_cannot_release", "never")
	}
	return p.BeamID, nil
}

func (c *Client) postV1(ctx context.Context, raw []byte, id string) (V1Receipt, error) {
	resp, err := c.request(ctx, "POST", v1Path, raw, nil)
	if err != nil && !asHTTP(err, 409) {
		return V1Receipt{}, err
	}
	r, parseErr := c.checkV1Receipt(resp.body, id)
	if err != nil && (parseErr != nil || r.Handoff == nil) {
		return V1Receipt{}, err
	}
	if parseErr != nil {
		return V1Receipt{}, parseErr
	}
	if !r.OK && r.Handoff == nil {
		return V1Receipt{}, failure("invalid_receipt", "after_reconcile")
	}
	return r, nil
}

// ShareV1 consults the same durable fence as released hooks, including after v2 release.
func (c *Client) ShareV1(ctx context.Context, data []byte) (V1Receipt, error) {
	id, err := legacyPacket(data)
	if err != nil {
		return V1Receipt{}, err
	}
	// Ordinary v1 hooks inspect an existing retirement fence. A plain upload
	// works without creating or requiring a local journal.
	exists, err := c.hasJournal()
	if err != nil {
		return V1Receipt{}, err
	}
	if exists {
		row, ok, err := c.LegacyRow(ctx, id)
		if err != nil {
			return V1Receipt{}, err
		}
		if ok {
			if len(row.Receipt) == 0 {
				return V1Receipt{}, failure("handoff_pending", "after_reconcile")
			}
			return c.checkV1Receipt(row.Receipt, id)
		}
	}
	r, err := c.postV1(ctx, data, id)
	if err != nil {
		return V1Receipt{}, err
	}
	if r.Handoff != nil {
		s, err := c.journal()
		if err != nil {
			return V1Receipt{}, err
		}
		if _, err := s.BeginLegacy(ctx, c.legacyEndpoint, id, data); err != nil {
			return V1Receipt{}, err
		}
		if err := c.ackV1(ctx, s, id, r); err != nil {
			return V1Receipt{}, err
		}
	}
	return r, nil
}
func (c *Client) ackV1(ctx context.Context, s *journal.Store, id string, r V1Receipt) error {
	// Store the same public fields as the JS sender. Destination session IDs already in the row survive.
	saved, err := json.Marshal(struct {
		BeamID  string     `json:"beamId"`
		URL     string     `json:"url"`
		Handoff *V1Handoff `json:"handoff"`
	}{r.BeamID, r.URL, r.Handoff})
	if err != nil {
		return err
	}
	return s.CompleteLegacy(ctx, c.legacyEndpoint, id, r.Handoff.ID, saved)
}

// HandoffV1 preserves the legacy command's frozen request and receiver-issued generation.
func (c *Client) HandoffV1(ctx context.Context, id string, data []byte, snapshotFallback bool) (V1Receipt, error) {
	if !beamIDRE.MatchString(id) {
		return V1Receipt{}, failure("invalid_beam_id", "never")
	}
	s, err := c.journal()
	if err != nil {
		return V1Receipt{}, err
	}
	if _, found, err := s.Transfer(ctx, c.legacyEndpoint, id); err != nil {
		return V1Receipt{}, err
	} else if found {
		return V1Receipt{}, failure("resume_with_v2", "after_reconcile")
	}
	row, ok, err := s.Legacy(ctx, c.legacyEndpoint, id)
	if err != nil {
		return V1Receipt{}, err
	}
	if !ok {
		got, err := legacyPacket(data)
		if err != nil || got != id {
			return V1Receipt{}, failure("selected_snapshot_required", "never")
		}
		row, err = s.BeginLegacy(ctx, c.legacyEndpoint, id, data)
		if err != nil {
			return V1Receipt{}, err
		}
	}
	if len(row.Receipt) > 0 {
		return c.checkV1Receipt(row.Receipt, id)
	}
	if got, err := legacyPacket(row.Payload); err != nil || got != id {
		return V1Receipt{}, failure("needs_reconciliation", "after_reconcile")
	}
	generation := row.PublicationID
	if generation == "" {
		r, err := c.postV1(ctx, row.Payload, id)
		if err != nil {
			return V1Receipt{}, err
		}
		if r.Handoff != nil {
			return r, c.ackV1(ctx, s, id, r)
		}
		if !beamIDRE.MatchString(r.PublicationID) {
			if snapshotFallback && r.OK && r.PublicationID == "" {
				if err := s.ForgetUnpublishedLegacy(ctx, c.legacyEndpoint, id); err != nil {
					return V1Receipt{}, err
				}
				r.SnapshotFallback = true
				return r, nil
			}
			return V1Receipt{}, failure("handoff_unsupported", "after_reconcile")
		}
		generation = r.PublicationID
		if err := s.SetLegacyPublication(ctx, c.legacyEndpoint, id, generation); err != nil {
			return V1Receipt{}, err
		}
	}
	if !beamIDRE.MatchString(generation) {
		return V1Receipt{}, failure("needs_reconciliation", "after_reconcile")
	}
	// The released JS sender appends the control member last. Preserve the frozen
	// snapshot's spelling and field order across a native-client restart.
	end := bytes.LastIndexByte(row.Payload, '}')
	body := append([]byte(nil), row.Payload[:end]...)
	body = append(body, `,"handoffId":"`...)
	body = append(body, generation...)
	body = append(body, '"')
	body = append(body, row.Payload[end:]...)
	r, err := c.postV1(ctx, body, id)
	if err != nil {
		return V1Receipt{}, err
	}
	if r.Handoff == nil || r.Handoff.ID != generation {
		return V1Receipt{}, failure("needs_reconciliation", "after_reconcile")
	}
	return r, c.ackV1(ctx, s, id, r)
}
