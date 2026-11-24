//go:build beam_openclaw

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Uses the same ordinary app-server protocol and persisted rollout as the
// native Codex product. All provider requests go to the synthetic local model.
type nativeMirrorThread struct {
	enc     *json.Encoder
	dec     *json.Decoder
	request int
	id      string
}

func startMirrorThread(t *testing.T, c cli, home, modelURL string) *nativeMirrorThread {
	t.Helper()
	codex := os.Getenv("BEAM_E2E_CODEX")
	if !filepath.IsAbs(codex) {
		t.Fatal("set BEAM_E2E_CODEX to the packaged native Codex executable")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model=\"gpt-5\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"fixture\"\nwire_api=\"responses\"\nbase_url=%q\nrequires_openai_auth=false\nsupports_websockets=false\n", modelURL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	cmd := exec.CommandContext(ctx, codex, "app-server")
	cmd.Dir = c.dir
	cmd.Env = append(append([]string{}, c.env...), "CODEX_HOME="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); stdin.Close(); _ = cmd.Wait() })
	thread := &nativeMirrorThread{enc: json.NewEncoder(stdin), dec: json.NewDecoder(stdout)}
	thread.call(t, "initialize", map[string]any{"clientInfo": map[string]string{"name": "beam-qa", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil)
	if err := thread.enc.Encode(map[string]string{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	var start struct{ Thread struct{ ID string } }
	thread.call(t, "thread/start", map[string]any{"cwd": c.dir, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": false}, &start)
	if start.Thread.ID == "" {
		t.Fatal("native Codex did not start a thread")
	}
	thread.id = start.Thread.ID
	return thread
}

type mirrorRPCMessage struct {
	ID                    int
	Method                string
	Result, Params, Error json.RawMessage
}

func (n *nativeMirrorThread) next(t *testing.T) mirrorRPCMessage {
	t.Helper()
	var m mirrorRPCMessage
	if err := n.dec.Decode(&m); err != nil {
		t.Fatal("read native Codex response:", err)
	}
	return m
}
func (n *nativeMirrorThread) call(t *testing.T, method string, params any, out any) {
	t.Helper()
	n.request++
	if err := n.enc.Encode(map[string]any{"id": n.request, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	for {
		m := n.next(t)
		if m.ID != n.request {
			continue
		}
		if len(m.Error) > 0 {
			t.Fatalf("native Codex %s rejected the synthetic request: %s", method, m.Error)
		}
		if out != nil {
			if err := json.Unmarshal(m.Result, out); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
}
func (n *nativeMirrorThread) turn(t *testing.T, text string) {
	t.Helper()
	n.call(t, "turn/start", map[string]any{"threadId": n.id, "input": []map[string]string{{"type": "text", "text": text}}}, nil)
	for {
		m := n.next(t)
		if m.Method != "turn/completed" {
			continue
		}
		var done struct {
			ThreadID string
			Turn     struct{ Status string }
		}
		if err := json.Unmarshal(m.Params, &done); err != nil {
			t.Fatal(err)
		}
		if done.ThreadID == n.id {
			if done.Turn.Status != "completed" {
				t.Fatalf("native Codex turn ended %s", done.Turn.Status)
			}
			return
		}
	}
}

func updateMirrorConfig(t *testing.T, g *gateway, edit func(map[string]any)) {
	t.Helper()
	var file string
	for _, v := range g.env {
		if name, location, ok := strings.Cut(v, "="); ok && name == "OPENCLAW_CONFIG_PATH" {
			file = location
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	edit(cfg)
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

type mirrorCatalog struct {
	Catalogs []struct {
		ID    string
		Hosts []struct {
			HostID   string
			Sessions []struct {
				ThreadID, SourceHomeID string
				ExportPolicy           *struct {
					Origin    string
					Automatic bool
				}
			}
		}
	}
}

// The real stdio proxy relays newline-delimited RPC bytes to this Unix socket.
// Each connection terminates in the native app-server with its own selected home.
func serveNativeMirrorSocket(t *testing.T, c cli, home string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "beam-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "app.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var active sync.WaitGroup
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			file, err := conn.File()
			conn.Close()
			if err != nil {
				return
			}
			cmd := exec.CommandContext(ctx, os.Getenv("BEAM_E2E_CODEX"), "app-server", "--stdio")
			cmd.Env = append(append([]string{}, c.env...), "CODEX_HOME="+home)
			cmd.Dir, cmd.Stdin, cmd.Stdout = c.dir, file, file
			err = cmd.Start()
			file.Close()
			if err != nil {
				return
			}
			active.Add(1)
			go func() { defer active.Done(); _ = cmd.Wait() }()
		}
	}()
	t.Cleanup(func() { cancel(); listener.Close(); <-closed; active.Wait() })
	return socket
}

type mirrorHead struct {
	PublicationID, Revision, Digest, State string
	CanRelease                             bool
}

func getMirrorHead(t *testing.T, url string) mirrorHead {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", url+"/head", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("read enrolled publication returned %d", res.StatusCode)
	}
	var h mirrorHead
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	return h
}
func waitForMirror(t *testing.T, url, previous string) mirrorHead {
	t.Helper()
	deadline := time.Now().Add(95 * time.Second)
	for {
		head := getMirrorHead(t, url)
		if head.Revision != "" && head.Revision != previous {
			return head
		}
		if time.Now().After(deadline) {
			t.Fatal("source mirror did not update the enrolled generation")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestOpenClawCodexV2Mirror(t *testing.T) {
	plugin := os.Getenv("BEAM_E2E_CODEX_PLUGIN")
	if !filepath.IsAbs(plugin) {
		t.Fatal("set BEAM_E2E_CODEX_PLUGIN to the packaged @openclaw/codex npm tarball")
	}
	c := newCLI(t)
	models := new(modelCalls)
	model := httptest.NewServer(models)
	t.Cleanup(model.Close)
	receiver := newGateway(t, c, model.URL)
	receiver.enableBeam(t)
	receiver.start(t)
	published := receiver.proxy(t, "publisher@example.test", "192.0.2.10", nil)
	teammate := receiver.proxy(t, "teammate@example.test", "192.0.2.11", nil)
	const original = "synthetic-original-publisher"
	const rotated = "synthetic-replacement-publisher"
	var revoked, owner atomic.Bool
	var creates atomic.Int32
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		origin := published.Config.Handler
		switch req.Header.Get("Authorization") {
		case "Bearer " + original:
			if revoked.Load() {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
		case "Bearer " + rotated:
			if !owner.Load() {
				origin = teammate.Config.Handler
			}
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if req.Method == http.MethodPut && req.Header.Get("If-None-Match") == "*" {
			creates.Add(1)
		}
		req.Header.Del("Authorization")
		origin.ServeHTTP(w, req)
	}))
	t.Cleanup(edge.Close)
	home := filepath.Join(privateTempDir(t), "codex")
	thread := startMirrorThread(t, c, home, model.URL)
	thread.turn(t, "First native mirror turn.")
	source := newGateway(t, c, model.URL)
	source.env = append(source.env, "CODEX_HOME="+home)
	// Beam ships with the Gateway. Its native Codex catalog is a separate plugin
	// installed into this source profile, with the same managed npm layout as a registry install.
	source.cli(t, "plugins", "install", "npm-pack:"+plugin, "--force", "--accept-capabilities")
	source.enableBeam(t)
	const localToken = "synthetic-mirror-administrator"
	updateMirrorConfig(t, source, func(cfg map[string]any) {
		cfg["gateway"].(map[string]any)["auth"] = map[string]string{"mode": "token", "token": localToken}
		cfg["gateway"].(map[string]any)["roles"] = map[string]any{"default": "mirror-operator", "definitions": map[string]any{"mirror-operator": map[string]any{"scopes": []string{"operator.admin", "operator.read", "operator.write"}, "agents": "*", "sessions": map[string]string{"others": "write"}}}}
		plugins := cfg["plugins"].(map[string]any)
		entries := plugins["entries"].(map[string]any)
		entries["codex"] = map[string]any{"enabled": true, "config": map[string]any{"appServer": map[string]string{"homeScope": "user"}}}
		entries["beam"] = map[string]any{"enabled": true, "config": map[string]any{"mirror": map[string]any{"endpoint": edge.URL + "/api/v2/beam", "token": original, "catalogs": []string{"codex"}, "pollSeconds": 10}}}
	})
	source.start(t)
	rpcArgs := func(method string, params any) []string {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		return []string{"gateway", "call", method, "--url", "ws://127.0.0.1:" + source.port, "--token", localToken, "--params", string(raw), "--timeout", "45000", "--json"}
	}
	rpc := func(method string, params any, out any) {
		t.Helper()
		data := source.cli(t, rpcArgs(method, params)...)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal("read Gateway RPC result:", err)
		}
	}
	var catalog mirrorCatalog
	rpc("sessions.catalog.list", map[string]any{"catalogId": "codex", "agentId": "main", "limitPerHost": 50}, &catalog)
	var selection map[string]string
	for _, cat := range catalog.Catalogs {
		for _, host := range cat.Hosts {
			for _, row := range host.Sessions {
				if row.ThreadID == thread.id && row.SourceHomeID != "" && row.ExportPolicy != nil && row.ExportPolicy.Automatic && row.ExportPolicy.Origin == "native-local" {
					selection = map[string]string{"catalogId": cat.ID, "hostId": host.HostID, "threadId": row.ThreadID, "agentId": "main", "sourceHomeId": row.SourceHomeID}
				}
			}
		}
	}
	if selection == nil {
		t.Fatal("managed local Codex did not list the native thread with an eligible full locator")
	}
	t.Log("native Gateway catalog row: origin=native-local, automatic=true")
	var enrollment struct {
		State, BeamID string
		Head          struct{ PublicationID, Revision string }
	}
	rpc("beam.mirror.enroll", map[string]any{"source": selection}, &enrollment)
	if enrollment.State != "published" || enrollment.BeamID == "" || enrollment.Head.PublicationID == "" {
		t.Fatalf("independent enrollment failed: %s", enrollment.State)
	}
	url := published.URL + "/api/v2/beam/snapshots/" + enrollment.BeamID
	first := getMirrorHead(t, url)
	if first.PublicationID != enrollment.Head.PublicationID || first.Revision != enrollment.Head.Revision {
		t.Fatal("enrollment did not return receiver-issued generation and revision")
	}
	source.stop()
	source.cli(t, "plugins", "disable", "beam")
	source.start(t)
	thread.turn(t, "Second native turn during source disable.")
	if current := getMirrorHead(t, url); current.Revision != first.Revision {
		t.Fatal("disabled source changed the remote publication")
	}
	source.stop()
	revoked.Store(true)
	source.cli(t, "plugins", "enable", "beam")
	updateMirrorConfig(t, source, func(cfg map[string]any) {
		cfg["plugins"].(map[string]any)["entries"].(map[string]any)["beam"].(map[string]any)["config"].(map[string]any)["mirror"].(map[string]any)["token"] = rotated
	})
	source.start(t)
	confirm := map[string]any{"source": selection, "confirmGeneration": first.PublicationID}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	denied := exec.CommandContext(ctx, source.binary, rpcArgs("beam.mirror.enroll", confirm)...)
	denied.Dir, denied.Env = source.dir, source.env
	output, err := denied.CombinedOutput()
	cancel()
	if err == nil || !bytes.Contains(output, []byte("Beam enrollment did not complete")) {
		t.Fatal("read-only teammate unexpectedly reauthorized the first publisher")
	}
	if current := getMirrorHead(t, url); current.Revision != first.Revision {
		t.Fatal("refused credential rotation changed the receiver")
	}
	owner.Store(true)
	rpc("beam.mirror.enroll", confirm, &enrollment)
	if enrollment.State != "published" || enrollment.Head.PublicationID != first.PublicationID || enrollment.Head.Revision != first.Revision {
		t.Fatal("replacement publisher credential failed to retain the same generation")
	}
	updated := waitForMirror(t, url, first.Revision)
	if updated.PublicationID != first.PublicationID {
		t.Fatal("re-enabled source published a different generation")
	}
	response, err := http.Get(published.URL + "/api/v2/beam/publications/" + updated.PublicationID + "/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 60<<10))
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte("Second native turn during source disable.")) {
		t.Fatal("updated snapshot omitted the actual second Codex turn")
	}
	releaseBody, _ := json.Marshal(map[string]string{"digest": updated.Digest})
	req, err := http.NewRequestWithContext(t.Context(), "POST", published.URL+"/api/v2/beam/publications/"+updated.PublicationID+"/release", bytes.NewReader(releaseBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", updated.Revision)
	req.Header.Set("Idempotency-Key", "10000000-0000-4000-8000-000000000009")
	result, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	result.Body.Close()
	if result.StatusCode/100 != 2 {
		t.Fatalf("receiver release returned %d", result.StatusCode)
	}
	released := getMirrorHead(t, url)
	if released.State != "released" {
		t.Fatal("receiver did not release the generation")
	}
	source.stop()
	source.start(t)
	thread.turn(t, "Third native turn after receiver release.")
	// Give the service two normal scheduled polls after release and restart.
	time.Sleep(22 * time.Second)
	if final := getMirrorHead(t, url); final.PublicationID != first.PublicationID || final.State != "released" || final.Revision != released.Revision {
		t.Fatal("sender reopened or modified the released publication")
	}
	if creates.Load() != 1 {
		t.Fatalf("expected the single original creation, got %d", creates.Load())
	}
	const importedID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	packet, _ := json.Marshal(map[string]any{"version": 1, "beamId": importedID, "source": "codex", "title": "Synthetic imported packet", "updatedAt": time.Now().UTC().Format(time.RFC3339), "completed": true, "items": []any{map[string]any{"type": "userMessage", "text": "Synthetic imported message"}}})
	ingest, err := http.NewRequestWithContext(t.Context(), "POST", "http://127.0.0.1:"+source.port+"/api/v1/beam/sessions", bytes.NewReader(packet))
	if err != nil {
		t.Fatal(err)
	}
	ingest.Header.Set("Authorization", "Bearer "+localToken)
	ingest.Header.Set("Content-Type", "application/json")
	accepted, err := http.DefaultClient.Do(ingest)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Body.Close()
	if accepted.StatusCode/100 != 2 {
		t.Fatalf("legacy receiver could not register imported fixture: %d", accepted.StatusCode)
	}
	var imported mirrorCatalog
	rpc("sessions.catalog.list", map[string]any{"catalogId": "beam", "agentId": "main"}, &imported)
	foundImport := false
	for _, catalog := range imported.Catalogs {
		for _, host := range catalog.Hosts {
			for _, row := range host.Sessions {
				if row.ThreadID == importedID && row.ExportPolicy != nil && row.ExportPolicy.Origin == "imported" && !row.ExportPolicy.Automatic {
					foundImport = true
				}
			}
		}
	}
	if !foundImport {
		t.Fatal("Gateway catalog did not expose ineligible imported provenance")
	}
	t.Log("imported Gateway catalog row: origin=imported, automatic=false")
	if runtime.GOOS != "windows" {
		source.stop()
		socket := serveNativeMirrorSocket(t, c, home)
		updateMirrorConfig(t, source, func(cfg map[string]any) {
			appServer := cfg["plugins"].(map[string]any)["entries"].(map[string]any)["codex"].(map[string]any)["config"].(map[string]any)["appServer"].(map[string]any)
			appServer["args"] = []string{"app-server", "proxy", "--sock", socket}
		})
		source.start(t)
		var proxied mirrorCatalog
		rpc("sessions.catalog.list", map[string]any{"catalogId": "codex", "agentId": "main"}, &proxied)
		foundProxy := false
		for _, catalog := range proxied.Catalogs {
			for _, host := range catalog.Hosts {
				for _, row := range host.Sessions {
					if row.ThreadID == thread.id && row.ExportPolicy == nil {
						foundProxy = true
					}
				}
			}
		}
		if !foundProxy {
			t.Fatal("managed Codex proxy did not list the real thread as unclassified")
		}
		t.Log("proxied Gateway catalog row: exportPolicy absent")
		source.stop()
	}
	if len(models.seen()) != 3 {
		t.Fatalf("expected only three explicit native model turns, got %d", len(models.seen()))
	}
}
