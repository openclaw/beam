package receiver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/openclaw/beam/internal/openclawcli"
)

type gatewayCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

func (c *Client) resolveHuman(ctx context.Context, raw, profile string) (string, error) {
	u, err := trustedLink(c.base, raw, true)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(u.Path, c.base.Path+"/")
	var id string
	if name, ok := strings.CutPrefix(rel, "beam/"); ok {
		match := prettyURL.FindStringSubmatch(name)
		if len(match) != 3 || len(match[1]) > 48 || u.RawQuery != "" {
			return "", failure("invalid_receiver_link", "never")
		}
		id = match[2]
	} else if strings.HasPrefix(rel, "chat/") {
		id = u.Query().Get("thread")
		if !beamIDRE.MatchString(id) {
			return "", failure("invalid_receiver_link", "never")
		}
		b, _ := json.Marshal(V1Receipt{BeamID: id, URL: u.String()})
		if _, err := c.checkV1Receipt(b, id); err != nil {
			return "", err
		}
	} else {
		return "", failure("full_machine_link_required", "never")
	}
	if len(id) != 32 {
		if profile == "" {
			return "", failure("remote_profile_required_for_short_link", "never")
		}
		cli, err := openclawcli.New(ctx, profile, c.legacyEndpoint, "")
		if err != nil {
			return "", err
		}
		id, err = resolvePrefix(ctx, cli, id)
		if err != nil {
			return "", err
		}
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return "", err
	}
	if err := require(caps, "read"); err != nil {
		return "", err
	}
	pub, found, err := c.head(ctx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", failure("unavailable", "never")
	}
	return pub.APIURL, nil
}

func resolvePrefix(ctx context.Context, gw gatewayCaller, prefix string) (string, error) {
	var route struct {
		CatalogID    string            `json:"catalogId"`
		RouteSegment string            `json:"routeSegment"`
		Methods      map[string]string `json:"methods"`
	}
	raw, err := gw.Call(ctx, "sessions.catalog.route", map[string]string{"routeSegment": "beam"})
	if err != nil {
		return "", err
	}
	if json.Unmarshal(raw, &route) != nil || route.CatalogID != "beam" || route.RouteSegment != "beam" {
		return "", failure("unavailable", "never")
	}
	method := route.Methods["list"]
	if method != "beam.owned.list" && method != "sessions.catalog.list" {
		return "", failure("unavailable", "never")
	}
	raw, err = gw.Call(ctx, method, map[string]any{"catalogId": "beam", "search": prefix, "limitPerHost": 2})
	if err != nil {
		return "", err
	}
	var data struct {
		Catalogs []struct {
			ID         string                                `json:"id"`
			Pending    bool                                  `json:"pending"`
			Error      *struct{}                             `json:"error"`
			ShareRoute struct{ RouteSegment, HostID string } `json:"shareRoute"`
			Hosts      []struct {
				HostID     string    `json:"hostId"`
				NextCursor string    `json:"nextCursor"`
				Error      *struct{} `json:"error"`
				Sessions   []struct {
					ThreadID string `json:"threadId"`
				} `json:"sessions"`
			} `json:"hosts"`
		} `json:"catalogs"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return "", failure("invalid_catalog_response", "never")
	}
	matches := 0
	var id string
	catalogs, hosts := 0, 0
	for _, cat := range data.Catalogs {
		if cat.ID != "beam" || cat.ShareRoute.RouteSegment != "beam" {
			continue
		}
		catalogs++
		if cat.Pending || cat.Error != nil {
			return "", failure("unavailable", "never")
		}
		for _, host := range cat.Hosts {
			if host.HostID != cat.ShareRoute.HostID {
				continue
			}
			hosts++
			if host.NextCursor != "" || host.Error != nil {
				return "", failure("ambiguous_or_incomplete_link", "never")
			}
			for _, session := range host.Sessions {
				if beamIDRE.MatchString(session.ThreadID) && strings.HasPrefix(session.ThreadID, prefix) {
					matches++
					id = session.ThreadID
				}
			}
		}
	}
	if catalogs != 1 || hosts != 1 || matches != 1 {
		return "", failure("ambiguous_or_unavailable_link", "never")
	}
	return id, nil
}
