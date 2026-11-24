package receiver

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

type Instruction struct {
	RequestID string `json:"requestId"`
	Text      string `json:"instruction"`
	Agent     string `json:"agent,omitempty"`
}

type SendReceipt struct {
	RequestID   string       `json:"requestId"`
	State       string       `json:"state"`
	Destination *Destination `json:"destination,omitempty"`
}

func parseSendReceipt(raw []byte, id string) (SendReceipt, error) {
	var r SendReceipt
	if json.Unmarshal(raw, &r) != nil || r.RequestID != id || !slices.Contains([]string{"pending", "accepted", "completed", "needs_reconciliation"}, r.State) {
		return r, failure("invalid_send_receipt", "after_reconcile")
	}
	if r.State == "needs_reconciliation" {
		return r, failure("needs_reconciliation", "after_reconcile")
	}
	return r, nil
}

func (c *Client) sendStatus(ctx context.Context, publication, id string) (SendReceipt, error) {
	r, err := c.request(ctx, "GET", v2Path+"/publications/"+publication+"/requests/"+id, nil, nil)
	if err != nil {
		return SendReceipt{}, err
	}
	return parseSendReceipt(r.body, id)
}

func (c *Client) Continue(ctx context.Context, link string, in Instruction) (SendReceipt, error) {
	publication, err := c.publicationID(link)
	if err != nil {
		return SendReceipt{}, err
	}
	if strings.TrimSpace(in.Text) == "" || !utf8.ValidString(in.Text) || len(in.Text) > 48*1024 || in.Agent != "" && !agentName.MatchString(in.Agent) {
		return SendReceipt{}, failure("invalid_instruction", "never")
	}
	if in.RequestID != "" && !uuidRE.MatchString(in.RequestID) {
		return SendReceipt{}, failure("invalid_request_id", "never")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return SendReceipt{}, err
	}
	if err := require(caps, "continue"); err != nil {
		return SendReceipt{}, err
	}
	if caps.SendReceiptRetentionSeconds <= 0 {
		return SendReceipt{}, failure("send_recovery_unavailable", "never")
	}
	s, err := c.journal()
	if err != nil {
		return SendReceipt{}, err
	}
	if in.RequestID != "" {
		resource, digest, found, err := s.Sent(ctx, c.legacyEndpoint, in.RequestID)
		if err != nil {
			return SendReceipt{}, err
		}
		if found {
			b, _ := json.Marshal(in)
			if resource != publication || digest != packet.Digest(b) {
				return SendReceipt{}, failure("request_identity_conflict", "never")
			}
			receipt, err := c.sendStatus(ctx, publication, in.RequestID)
			if asHTTP(err, 404) || asHTTP(err, 410) {
				return SendReceipt{}, failure("needs_reconciliation", "after_reconcile")
			}
			return receipt, err
		}
	}
	p, found, err := s.Pending(ctx, c.legacyEndpoint, "send", publication)
	if err != nil {
		return SendReceipt{}, err
	}
	if !found {
		id := in.RequestID
		if id == "" {
			id, err = newID()
			if err != nil {
				return SendReceipt{}, err
			}
		}
		wire := in
		wire.RequestID = id
		data, err := json.Marshal(wire)
		if err != nil {
			return SendReceipt{}, err
		}
		p, err = s.Begin(ctx, journal.Pending{Endpoint: c.legacyEndpoint, Kind: "send", Resource: publication, ID: id, Body: []byte(packet.Digest(data)), Created: time.Now()})
		if err != nil {
			return SendReceipt{}, err
		}
		found = p.ID != id
	}
	// The original instruction can contain private text. Persist only its digest and
	// reconstruct the same bytes from the caller's input for a retry.
	wire := in
	wire.RequestID = p.ID
	data, err := json.Marshal(wire)
	if err != nil {
		return SendReceipt{}, err
	}
	digest := packet.Digest(data)
	if string(p.Body) != digest || (in.RequestID != "" && in.RequestID != p.ID) {
		return SendReceipt{}, failure("pending_send_differs", "after_reconcile")
	}
	finish := func(receipt SendReceipt) (SendReceipt, error) {
		if receipt.State == "accepted" || receipt.State == "completed" {
			return receipt, s.ConfirmSend(ctx, p, digest)
		}
		return receipt, nil
	}
	if found {
		receipt, err := c.sendStatus(ctx, publication, p.ID)
		if err == nil {
			return finish(receipt)
		}
		if !asHTTP(err, 404) {
			return SendReceipt{}, err
		}
		age := time.Now().Unix() - p.Created.Unix()
		if age < 0 || age >= caps.SendReceiptRetentionSeconds {
			return SendReceipt{}, failure("needs_reconciliation", "after_reconcile")
		}
	}
	r, err := c.request(ctx, "POST", v2Path+"/publications/"+publication+"/continue", data, http.Header{"Idempotency-Key": {p.ID}})
	if err != nil {
		return SendReceipt{}, err
	}
	receipt, err := parseSendReceipt(r.body, p.ID)
	if err != nil {
		return SendReceipt{}, err
	}
	return finish(receipt)
}
