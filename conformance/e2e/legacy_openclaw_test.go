//go:build beam_openclaw

package e2e

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// Use the released writer first. Hand-assembled SQLite records cannot establish
// what the stable parser, actor projection, and storage adapter actually wrote.
func TestReleasedOpenClawSnapshotUpgrade(t *testing.T) {
	old := os.Getenv("BEAM_E2E_OLD_OPENCLAW")
	if old == "" {
		t.Skip("set BEAM_E2E_OLD_OPENCLAW to a released OpenClaw 2026.11.7 or 2026.11.8 executable")
	}
	candidate := os.Getenv("BEAM_E2E_OPENCLAW")
	t.Setenv("BEAM_E2E_OPENCLAW", old)
	c := newCLI(t)
	g := newGateway(t, c, "http://127.0.0.1:1")
	version := strings.TrimSpace(string(g.cli(t, "--version")))
	fields := strings.Fields(version)
	if !slices.Contains(fields, "2026.11.7") && !slices.Contains(fields, "2026.11.8") {
		t.Fatalf("expected a released pre-generation writer, got %q", version)
	}
	t.Logf("released writer: %s", version)
	g.cli(t, "plugins", "enable", "beam")
	g.start(t)
	edge := g.proxy(t, "publisher@example.test", "192.0.2.10", nil)
	const id = "00000000000000000000000000001107"
	const hook = "LegacyFixtureHook"
	const marker = "This text came from the released Gateway."
	payload := map[string]any{
		"version": 1, "beamId": id, "source": "fixture", "title": "Legacy snapshot",
		"updatedAt": "2026-11-07T12:30:00Z", "completed": true, "hookEvent": hook,
		"items": []map[string]string{{"type": "userMessage", "text": marker}},
	}
	packet, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(c.dir, "legacy.json")
	if err := os.WriteFile(file, packet, 0600); err != nil {
		t.Fatal(err)
	}
	var receipt struct{ BeamID string }
	machineResult(t, c.run(t, nil, "share", file, "--to", edge.URL+"/api/v1/beam/sessions", "--json"), &receipt)
	if receipt.BeamID != id {
		t.Fatal("old receiver did not acknowledge the selected snapshot")
	}
	g.stop()
	dbPath := filepath.Join(filepath.Dir(g.log), "state", "openclaw.sqlite")
	readRow := func() []byte {
		t.Helper()
		uri := url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}
		db, err := sql.Open("sqlite", uri.String())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var raw []byte
		err = db.QueryRowContext(t.Context(), "SELECT value_json FROM plugin_state_entries WHERE plugin_id='beam' AND namespace='sessions' AND entry_key=?", id).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	baseline := readRow()
	var stored map[string]json.RawMessage
	if json.Unmarshal(baseline, &stored) != nil || stored["publicationId"] != nil || stored["publisherProfileId"] != nil || string(stored["hookEvent"]) != fmt.Sprintf("%q", hook) || !bytes.Contains(stored["items"], []byte(marker)) {
		t.Fatal("stable writer did not produce the expected pre-generation row")
	}
	var attribution string
	if json.Unmarshal(stored["uploaderProfileId"], &attribution) != nil || attribution == "" {
		t.Fatal("stable writer did not retain the proxy-attributed uploader")
	}
	t.Run("read_after_upgrade", func(t *testing.T) {
		if candidate == "" {
			t.Skip("set BEAM_E2E_OPENCLAW to the candidate executable")
		}
		g.binary = candidate
		g.start(t)
		headURL := edge.URL + "/api/v2/beam/snapshots/" + id + "/head"
		resp, err := edge.Client().Get(headURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var head struct {
			State, Digest, Revision, PublicationID, APIURL, SnapshotURL string
			CanRelease                                                  *bool
		}
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&head) != nil || head.State != "legacy_unbound" || head.SnapshotURL == "" || head.PublicationID != "" || head.APIURL != "" || head.CanRelease == nil || *head.CanRelease || head.Revision == "" {
			t.Fatalf("upgrade head status=%d state=%q release=%v", resp.StatusCode, head.State, head.CanRelease)
		}
		unconditional, err := edge.Client().Get(edge.URL + "/api/v2/beam/snapshots/" + id + "/snapshot")
		if err != nil {
			t.Fatal(err)
		}
		unconditional.Body.Close()
		if unconditional.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("unconditional legacy snapshot returned %d", unconditional.StatusCode)
		}
		raw := c.run(t, nil, "pull", edge.URL+"/beam/"+id, "--to", edge.URL)
		var projection struct {
			Version int
			BeamID  string
		}
		if json.Unmarshal(raw, &projection) != nil || projection.Version != 2 || projection.BeamID != id || !bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte(hook)) || bytes.Contains(raw, []byte("uploaderProfileId")) || fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != head.Digest {
			t.Fatal("conditional read did not match the selected legacy projection")
		}
		g.stop()
		if !bytes.Equal(readRow(), baseline) {
			t.Fatal("read-only upgrade changed the original snapshot")
		}
	})
}

// Follow the published quickstart against the released v1 Gateway. The input
// comes from OpenCode running through acpx and contains only synthetic turns.
func TestACPXToReleasedOpenClaw(t *testing.T) {
	input := os.Getenv("BEAM_E2E_ACPX_PACKET")
	if input == "" {
		t.Skip("set BEAM_E2E_ACPX_PACKET to the native OpenCode ACP test's sample")
	}
	c := newCLI(t)
	models := new(modelCalls)
	server := httptest.NewServer(models)
	defer server.Close()
	g := newGateway(t, c, server.URL)
	const token = "synthetic-gateway-token-for-this-test"
	g.cli(t, "config", "set", "gateway.auth", `{"mode":"token","token":"`+token+`"}`, "--strict-json")
	g.cli(t, "plugins", "enable", "beam")
	g.start(t)

	file := filepath.Join(c.dir, "task.v1.json")
	c.run(t, nil, "convert", input, "--packet-version", "1", "--out", file)
	raw := c.run(t, nil, "inspect", file)
	var selected struct {
		Version        int
		BeamID, Source string
		Items          []struct{ Type, Text string }
	}
	if err := json.Unmarshal(raw, &selected); err != nil || selected.Version != 1 || selected.Source != "acpx" || len(selected.Items) != 2 ||
		!strings.Contains(selected.Items[0].Text, sourceMarker) || !strings.Contains(selected.Items[0].Text, "[REDACTED_EMAIL]") ||
		selected.Items[1].Text != "The fixture received your authorized prompt." {
		t.Fatal("expected the reviewed native acpx:opencode fixture")
	}
	base := "http://127.0.0.1:" + g.port
	c.env = append(c.env, "BEAM_AUTH_TOKEN="+token)
	var receipt struct {
		OK          bool
		BeamID, URL string
	}
	machineResult(t, c.run(t, nil, "share", file, "--to", base+"/api/v1/beam/sessions", "--json"), &receipt)
	if !receipt.OK || receipt.BeamID != selected.BeamID || !strings.HasPrefix(receipt.URL, base+"/beam/") {
		t.Fatalf("unexpected OpenClaw receipt: %+v", receipt)
	}
	g.stop()
	uri := url.URL{Scheme: "file", Path: filepath.Join(filepath.Dir(g.log), "state", "openclaw.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var row []byte
	if err := db.QueryRow("SELECT value_json FROM plugin_state_entries WHERE plugin_id='beam' AND namespace='sessions' AND entry_key=?", selected.BeamID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Source string
		Items  []struct{ Type, Text string }
	}
	if err := json.Unmarshal(row, &saved); err != nil || saved.Source != selected.Source || !slices.Equal(saved.Items, selected.Items) || len(models.seen()) != 0 {
		t.Fatal("released Gateway did not retain the reviewed turns passively")
	}
}
