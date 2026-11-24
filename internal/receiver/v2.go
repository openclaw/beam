package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

type Capabilities struct {
	Protocol                    int      `json:"protocol"`
	PacketVersions              []int    `json:"packetVersions"`
	Operations                  []string `json:"operations"`
	MaxBodyBytes                int      `json:"maxBodyBytes"`
	SendReceiptRetentionSeconds int64    `json:"sendReceiptRetentionSeconds"`
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	if c.legacy {
		return Capabilities{}, failure("legacy_endpoint", "never")
	}
	r, err := c.request(ctx, "GET", v2Path+"/capabilities", nil, nil)
	if err != nil {
		return Capabilities{}, err
	}
	var caps Capabilities
	if json.Unmarshal(r.body, &caps) != nil || caps.Protocol != 2 || !slices.Contains(caps.PacketVersions, 2) || caps.MaxBodyBytes <= 0 {
		return Capabilities{}, failure("unsupported_receiver", "never")
	}
	return caps, nil
}
func require(c Capabilities, op string) error {
	if !slices.Contains(c.Operations, op) {
		return failure("unsupported_capability", "never")
	}
	return nil
}

type Publication struct {
	BeamID        string `json:"beamId"`
	PublicationID string `json:"publicationId"`
	Revision      string `json:"revision"`
	Digest        string `json:"digest"`
	Disposition   string `json:"disposition,omitempty"`
	URL           string `json:"url"`
	APIURL        string `json:"apiUrl"`
	CanRelease    bool   `json:"canRelease"`
	State         string `json:"state,omitempty"`
}

var etagRE = regexp.MustCompile(`^"[!#-~]{1,254}"$`)
var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (c *Client) checkPublication(pub *Publication, id string) error {
	if pub.BeamID != id || !opaqueRE.MatchString(pub.PublicationID) || !etagRE.MatchString(pub.Revision) || strings.Contains(pub.Revision[1:len(pub.Revision)-1], "\"") || !digestRE.MatchString(pub.Digest) {
		return failure("invalid_publication", "after_reconcile")
	}
	view, err := trustedLink(c.base, pub.URL, false)
	if err != nil {
		return err
	}
	api, err := trustedLink(c.base, pub.APIURL, false)
	if err != nil {
		return err
	}
	if api.Path != c.base.Path+v2Path+"/publications/"+pub.PublicationID {
		return failure("invalid_publication_link", "never")
	}
	pub.URL = view.String()
	pub.APIURL = api.String()
	return nil
}

func (c *Client) publicationID(link string) (string, error) {
	u, err := trustedLink(c.base, link, false)
	if err != nil {
		return "", err
	}
	id, ok := strings.CutPrefix(u.Path, c.base.Path+v2Path+"/publications/")
	if !ok || !opaqueRE.MatchString(id) {
		return "", failure("full_machine_link_required", "never")
	}
	return id, nil
}

func (c *Client) head(ctx context.Context, id string) (Publication, bool, error) {
	r, err := c.request(ctx, "GET", v2Path+"/snapshots/"+id+"/head", nil, nil)
	if asHTTP(err, 404) {
		return Publication{}, false, nil
	}
	if err != nil {
		return Publication{}, false, err
	}
	var pub Publication
	if json.Unmarshal(r.body, &pub) != nil {
		return pub, false, failure("invalid_head", "after_reconcile")
	}
	if err := c.checkPublication(&pub, id); err != nil {
		return pub, false, err
	}
	return pub, true, nil
}

func (c *Client) Share(ctx context.Context, data []byte) (Publication, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return Publication{}, err
	}
	return c.share(ctx, caps, data)
}
func (c *Client) share(ctx context.Context, caps Capabilities, data []byte) (Publication, error) {
	if err := require(caps, "publish"); err != nil {
		return Publication{}, err
	}
	if len(data) > caps.MaxBodyBytes {
		return Publication{}, failure("too_large", "never")
	}
	p, err := packet.Parse(data)
	if err != nil {
		return Publication{}, failure("invalid_packet", "never")
	}
	s, err := c.journal()
	if err != nil {
		return Publication{}, err
	}
	if _, found, err := s.Legacy(ctx, c.legacyEndpoint, p.BeamID); err != nil {
		return Publication{}, err
	} else if found {
		return Publication{}, failure("source_retired", "after_reconcile")
	}
	digest := packet.Digest(data)
	pending, found, err := s.Pending(ctx, c.legacyEndpoint, "publish", p.BeamID)
	if err != nil {
		return Publication{}, err
	}
	if !found {
		head, exists, err := c.head(ctx, p.BeamID)
		if err != nil {
			return Publication{}, err
		}
		condition := "*"
		if exists {
			if head.State == "released" || head.State == "ready" || head.State == "released_unbound" {
				return Publication{}, failure("source_retired", "after_reconcile")
			}
			if head.Digest == digest {
				head.Disposition = "unchanged"
				return head, nil
			}
			condition = head.Revision
		}
		key, err := newID()
		if err != nil {
			return Publication{}, err
		}
		pending, err = s.Begin(ctx, journal.Pending{Endpoint: c.legacyEndpoint, Kind: "publish", Resource: p.BeamID, ID: key, Body: data, Condition: condition, Created: time.Now()})
		if err != nil {
			return Publication{}, err
		}
	}
	if !bytes.Equal(data, pending.Body) {
		return Publication{}, failure("pending_publish_differs", "after_reconcile")
	}
	h := http.Header{"Idempotency-Key": {pending.ID}}
	if pending.Condition == "*" {
		h.Set("If-None-Match", "*")
	} else {
		h.Set("If-Match", pending.Condition)
	}
	r, err := c.request(ctx, "PUT", v2Path+"/snapshots/"+p.BeamID, pending.Body, h)
	if err != nil {
		// A definite precondition refusal made no change. A lost reply keeps the exact original request.
		if asHTTP(err, 412) {
			if dropErr := s.Clear(ctx, pending); dropErr != nil {
				return Publication{}, dropErr
			}
		}
		return Publication{}, err
	}
	var pub Publication
	if json.Unmarshal(r.body, &pub) != nil || pub.Digest != digest || !slices.Contains([]string{"applied", "unchanged"}, pub.Disposition) {
		return Publication{}, failure("invalid_publish_receipt", "after_reconcile")
	}
	if err := c.checkPublication(&pub, p.BeamID); err != nil {
		return Publication{}, err
	}
	return pub, s.Clear(ctx, pending)
}

type Release struct {
	PublicationID string `json:"publicationId"`
	Revision      string `json:"revision"`
	State         string `json:"state"`
	URL           string `json:"url"`
	APIURL        string `json:"apiUrl"`
}

func (c *Client) Handoff(ctx context.Context, id string, data []byte) (Release, error) {
	if !beamIDRE.MatchString(id) {
		return Release{}, failure("invalid_beam_id", "never")
	}
	s, err := c.journal()
	if err != nil {
		return Release{}, err
	}
	tr, found, err := s.Transfer(ctx, c.legacyEndpoint, id)
	if err != nil {
		return Release{}, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return Release{}, err
	}
	if !found {
		if err := require(caps, "release"); err != nil {
			return Release{}, err
		}
		p, err := packet.Parse(data)
		if err != nil || p.BeamID != id {
			return Release{}, failure("selected_snapshot_required", "never")
		}
		pub, err := c.share(ctx, caps, data)
		if err != nil {
			return Release{}, err
		}
		if !pub.CanRelease {
			return Release{}, failure("publisher_cannot_release", "never")
		}
		key, err := newID()
		if err != nil {
			return Release{}, err
		}
		tr, err = s.BeginTransfer(ctx, journal.Transfer{Endpoint: c.legacyEndpoint, BeamID: id, ID: key, Generation: pub.PublicationID, Revision: pub.Revision, Digest: pub.Digest, URL: pub.URL, APIURL: pub.APIURL, Packet: data})
		if err != nil {
			return Release{}, err
		}
	}
	if len(data) > 0 && packet.Digest(data) != tr.Digest {
		return Release{}, failure("pending_release_differs", "after_reconcile")
	}
	// Replay the same recorded intent even after a local acknowledgement. Only the
	// receiver can recheck the current caller's right to observe that publication.
	if err := require(caps, "release"); err != nil {
		return Release{}, err
	}
	body, _ := json.Marshal(struct {
		Digest string `json:"digest"`
	}{tr.Digest})
	r, err := c.request(ctx, "POST", v2Path+"/publications/"+tr.Generation+"/release", body, http.Header{"If-Match": {tr.Revision}, "Idempotency-Key": {tr.ID}})
	if err != nil {
		if asHTTP(err, 412) && len(tr.Receipt) == 0 {
			head, exists, readErr := c.head(ctx, id)
			if readErr == nil && exists && head.PublicationID == tr.Generation && head.State == "writable" && head.CanRelease {
				if clearErr := s.ForgetRefusedTransfer(ctx, tr); clearErr != nil {
					return Release{}, clearErr
				}
			}
		}
		return Release{}, err
	}
	var out Release
	if json.Unmarshal(r.body, &out) != nil || out.PublicationID != tr.Generation || out.Revision != tr.Revision || out.State != "released" {
		return out, failure("invalid_release_receipt", "after_reconcile")
	}
	view, err := trustedLink(c.base, out.URL, false)
	if err != nil {
		return out, err
	}
	out.URL = view.String()
	out.APIURL = tr.APIURL
	saved, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	return out, s.CompleteTransfer(ctx, tr, saved)
}

func (c *Client) Pull(ctx context.Context, link string) ([]byte, error) {
	id, err := c.publicationID(link)
	if err != nil {
		return nil, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if err = require(caps, "read"); err != nil {
		return nil, err
	}
	r, err := c.request(ctx, "GET", v2Path+"/publications/"+id+"/snapshot", nil, nil)
	if err != nil {
		return nil, err
	}
	if !etagRE.MatchString(r.header.Get("ETag")) || r.header.Get("Beam-Digest") != packet.Digest(r.body) {
		return nil, failure("invalid_snapshot_digest", "never")
	}
	if _, err := packet.Parse(r.body); err != nil {
		return nil, failure("invalid_packet", "never")
	}
	return r.body, nil
}

type Destination struct {
	State     string `json:"state"`
	Reference string `json:"reference,omitempty"`
}

func (c *Client) Resolve(ctx context.Context, link string) (Destination, error) {
	id, err := c.publicationID(link)
	if err != nil {
		return Destination{}, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return Destination{}, err
	}
	if err = require(caps, "resolve"); err != nil {
		return Destination{}, err
	}
	r, err := c.request(ctx, "GET", v2Path+"/publications/"+id+"/destination", nil, nil)
	if err != nil {
		return Destination{}, err
	}
	var out Destination
	if json.Unmarshal(r.body, &out) != nil || !slices.Contains([]string{"released_unbound", "creating", "ready", "needs_reconciliation"}, out.State) {
		return out, failure("invalid_destination", "after_reconcile")
	}
	return out, nil
}
