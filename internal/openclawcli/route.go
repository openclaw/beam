package openclawcli

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// A discovered method is one positional CLI argument. Plugin owners can
// register names unrelated to the catalog or public route segment.
var gatewayMethodName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,191}$`)

// ListRoute consumes the list operation attested by the selected Gateway for
// this caller and discovery response. It never accepts a remote method as input.
func (c *Client) ListRoute(ctx context.Context, segment, prefix string) (string, json.RawMessage, error) {
	if c.expectedURL == "" {
		return "", nil, errors.New("route lookup requires a pinned remote profile")
	}
	raw, err := c.Call(ctx, "sessions.catalog.route", map[string]string{"routeSegment": segment})
	if err != nil {
		return "", nil, err
	}
	var route struct {
		Catalog string `json:"catalogId"`
		Segment string `json:"routeSegment"`
		Methods struct {
			List string `json:"list"`
			Read string `json:"read"`
		} `json:"methods"`
	}
	if json.Unmarshal(raw, &route) != nil || route.Segment != segment || strings.TrimSpace(route.Catalog) == "" || len(route.Catalog) > 256 || !gatewayMethodName.MatchString(route.Methods.List) || !gatewayMethodName.MatchString(route.Methods.Read) {
		return "", nil, errors.New("catalog route is unavailable")
	}
	rows, err := c.callRegistered(ctx, route.Methods.List, map[string]any{"catalogId": route.Catalog, "search": prefix, "limitPerHost": 2})
	return route.Catalog, rows, err
}
