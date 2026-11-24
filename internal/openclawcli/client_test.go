package openclawcli

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestProfilePinsOriginBaseAndMethod(t *testing.T) {
	const endpoint = "https://team.example/base/api/v1/beam/sessions"
	for _, tc := range []struct {
		remote string
		ok     bool
	}{
		{"wss://team.example/base/", true}, {"wss://TEAM.example:443/base", true}, {"ws://team.example/base", false},
		{"wss://peer.example/base", false}, {"wss://team.example/other", false}, {"wss://team.example/base?forward=1", false}, {"wss://alice@team.example/base", false},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			var requests [][]string
			run := func(ctx context.Context, args []string) (json.RawMessage, error) {
				requests = append(requests, args)
				switch args[0] {
				case "config":
					if args[2] == "gateway.mode" {
						return json.RawMessage(`"remote"`), nil
					}
					val, _ := json.Marshal(tc.remote)
					return val, nil
				default:
					return json.RawMessage(`{"ok":true}`), nil
				}
			}
			client, err := newClient(context.Background(), endpoint, run)
			if (err == nil) != tc.ok {
				t.Fatalf("profile accepted %t, want %t, err %v", err == nil, tc.ok, err)
			}
			if !tc.ok {
				return
			}
			if _, err := client.Call(context.Background(), "gateway.unrelated", nil); err == nil || len(requests) != 2 {
				t.Fatal("admitted undeclared operation")
			}
			_, err = client.Call(context.Background(), "chat.history", map[string]any{"limit": 3})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"gateway", "call", "chat.history", "--expect-url", tc.remote, "--json", "--timeout", "120000", "--params", `{"limit":3}`}
			if !reflect.DeepEqual(requests[2], want) {
				t.Fatalf("args %v", requests[2])
			}
		})
	}
}

func TestRemoteProfileIsolation(t *testing.T) {
	got := profileEnvironment([]string{"PATH=original", "openclaw_gateway_token=old", "OPENCLAW_GATEWAY_URL=wrong", "OPENCLAW_STATE_DIR=wrong", "OPENCLAW_CONFIG_PATH=wrong"}, "dir", "config")
	want := []string{"PATH=original", "OPENCLAW_STATE_DIR=dir", "OPENCLAW_CONFIG_PATH=config"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected environment %v", got)
	}
}
