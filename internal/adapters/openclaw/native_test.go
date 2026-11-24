package openclaw

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/packet"
)

// Run only against a new loopback Gateway. The two synthetic sessions and the
// unauthenticated caller each use test-owned state. No model call is necessary.
func TestNativeOpenClawSource(t *testing.T) {
	if os.Getenv("BEAM_TEST_OPENCLAW_SOURCE") != "1" {
		t.Skip("set BEAM_TEST_OPENCLAW_SOURCE=1 with openclaw on PATH")
	}
	bin := os.Getenv("BEAM_E2E_BINARY")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "beam")
		if out, err := exec.Command("go", "build", "-o", bin, "../../../cmd/beam").CombinedOutput(); err != nil {
			t.Fatalf("build Beam: %v %.500s", err, out)
		}
	}
	exe, err := exec.LookPath("openclaw")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	profile := func(token string) ([]string, string) {
		home := t.TempDir()
		config := filepath.Join(home, "openclaw.json")
		body := map[string]any{
			"gateway":   map[string]any{"mode": "local", "port": port, "bind": "loopback", "auth": map[string]string{"mode": "token", "token": token}},
			"discovery": map[string]any{"mdns": map[string]string{"mode": "off"}},
			"agents":    map[string]any{"defaults": map[string]string{"workspace": filepath.Join(home, "workspace")}},
			"plugins":   map[string]bool{"enabled": false}, "browser": map[string]bool{"enabled": false}, "cron": map[string]bool{"enabled": false},
		}
		b, _ := json.Marshal(body)
		if err := os.WriteFile(config, b, 0600); err != nil {
			t.Fatal(err)
		}
		return []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "OPENCLAW_HOME=" + home,
			"XDG_CONFIG_HOME=" + home, "XDG_STATE_HOME=" + home, "XDG_CACHE_HOME=" + home,
			"OPENCLAW_STATE_DIR=" + home, "OPENCLAW_CONFIG_PATH=" + config, "OPENCLAW_GATEWAY_PORT=" + strconv.Itoa(port),
			"OPENCLAW_DISABLE_BONJOUR=1", "OPENCLAW_NO_RESPAWN=1", "OPENCLAW_SKIP_CHANNELS=1", "NO_COLOR=1",
		}, home
	}
	env, home := profile(hex.EncodeToString(token))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctxServer, stop := context.WithCancel(ctx)
	defer stop()
	gateway := exec.CommandContext(ctxServer, exe, "gateway", "run")
	gateway.Env, gateway.Dir = env, home
	log, err := os.Create(filepath.Join(home, "fixture-gateway.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	gateway.Stdout, gateway.Stderr = log, log
	if err = gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stop(); _ = gateway.Wait() }()
	client := http.Client{Timeout: time.Second}
	addr := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	ready := false
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		if resp, err := client.Get(addr); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		src, _ := os.ReadFile(log.Name())
		t.Fatalf("fixture failed to start: %.800s", strings.ReplaceAll(string(src), hex.EncodeToString(token), "[redacted]"))
	}
	run := func(env []string, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env, cmd.Dir = env, home
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if err != nil {
			return stdout, fmt.Errorf("%w: %.500s", err, strings.ReplaceAll(stderr.String(), hex.EncodeToString(token), "[redacted]"))
		}
		return stdout, nil
	}
	rpc := func(method string, params map[string]string) []byte {
		b, _ := json.Marshal(params)
		out, err := run(env, exe, "gateway", "call", method, "--json", "--params", string(b))
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return out
	}
	const selected = "agent:main:beam-selected"
	const other = "agent:main:beam-other"
	const marker = "portable synthetic conversation"
	const distractor = "foreign session marker"
	// Pair the fixture writer for its administrative chat.inject operation
	// before sessions.create negotiates a narrower device grant.
	rpc("config.schema", map[string]string{})
	for _, f := range []struct{ k, text string }{{selected, marker}, {other, distractor}} {
		rpc("sessions.create", map[string]string{"key": f.k})
		rpc("chat.inject", map[string]string{"sessionKey": f.k, "message": f.text})
	}
	reader, _ := profile(hex.EncodeToString(token))
	list, err := run(reader, bin, "list", "--from", "openclaw", "--json")
	if err != nil || !bytes.Contains(list, []byte(selected)) || !bytes.Contains(list, []byte(other)) {
		t.Fatalf("Beam failed to list both fixture sessions: %v %.200s", err, list)
	}
	params, _ := json.Marshal(map[string]string{"sessionKey": selected})
	history, err := run(reader, exe, "gateway", "call", "chat.history", "--json", "--params", string(params))
	if err != nil || !bytes.Contains(history, []byte(marker)) || bytes.Contains(history, []byte(distractor)) {
		t.Fatal("Gateway did not isolate the requested history")
	}
	pack := []string{"pack", "--from", "openclaw", "--session", selected}
	out, err := run(reader, bin, pack...)
	if err != nil {
		t.Fatal(err)
	}
	p, err := packet.Parse(out)
	if err != nil || len(p.Items) != 1 || p.Items[0].Type != "agentMessage" || !strings.Contains(p.Items[0].Text, marker) || bytes.Contains(out, []byte(distractor)) {
		t.Fatalf("unexpected exported packet %v %.300s", err, out)
	}
	var receipt struct {
		HasMore       *bool
		TotalMessages *int
	}
	if err := json.Unmarshal(history, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.HasMore == nil && receipt.TotalMessages == nil && !p.Truncated {
		t.Fatal("old Gateway without exhaustion receipts must report partial history")
	}
	guest, _ := profile("invalid-synthetic-gateway-token")
	denied, err := run(guest, bin, append(pack, "--json")...)
	var refusal adapter.Envelope
	if err == nil || json.Unmarshal(denied, &refusal) != nil || refusal.OK || refusal.Error == nil || adapter.ExitCode(refusal.Error) != 4 || refusal.Result != nil || bytes.Contains(denied, []byte(marker)) {
		t.Fatal("unauthenticated profile could export a packet")
	}
	version, err := run(env, exe, "--version")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gateway %s; private selection exported %d row, partial=%t, wrong-token export denied, adapter protocol=%d", strings.TrimSpace(string(version)), len(p.Items), p.Truncated, adapter.Protocol)
}
