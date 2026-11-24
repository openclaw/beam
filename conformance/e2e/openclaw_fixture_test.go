//go:build beam_openclaw

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type gateway struct {
	binary, port, log string
	env               []string
	dir               string
	cmd               *exec.Cmd
	done              chan error
}

func newGateway(t *testing.T, c cli, modelURL string) *gateway {
	t.Helper()
	bin := os.Getenv("BEAM_E2E_OPENCLAW")
	if !filepath.IsAbs(bin) {
		t.Fatal("set BEAM_E2E_OPENCLAW to the packaged OpenClaw executable")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	state := privateTempDir(t)
	config := map[string]any{
		"plugins": map[string]any{"slots": map[string]string{"memory": "none"}},
		"cron":    map[string]bool{"enabled": false},
		"gateway": map[string]any{"mode": "local", "trustedProxies": []string{"127.0.0.1"},
			"auth": map[string]any{"mode": "trusted-proxy", "trustedProxy": map[string]any{
				"allowLoopback": true, "userHeader": "x-forwarded-user", "requiredHeaders": []string{"x-forwarded-proto"},
				"allowUsers": []string{"publisher@example.test", "teammate@example.test"}}},
			"roles": map[string]any{"default": "member", "definitions": map[string]any{"member": map[string]any{
				"scopes": []string{"operator.read", "operator.write"}, "agents": "*", "sessions": map[string]string{"others": "write"}}}}},
		"agents": map[string]any{"defaults": map[string]any{"model": map[string]string{"primary": "fixture/echo"}, "models": map[string]any{"fixture/echo": map[string]any{"agentRuntime": map[string]string{"id": "openclaw"}}}, "skipBootstrap": true, "skills": []string{}}},
		"tools":  map[string]string{"profile": "minimal"},
		"models": map[string]any{"mode": "replace", "providers": map[string]any{"fixture": map[string]any{
			"baseUrl": modelURL + "/v1", "apiKey": "test-token-placeholder", "api": "openai-responses", "request": map[string]bool{"allowPrivateNetwork": true},
			"models": []any{map[string]any{"id": "echo", "name": "echo", "api": "openai-responses", "reasoning": false, "input": []string{"text"},
				"cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}}}}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(state, "openclaw.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	g := &gateway{binary: bin, port: port, dir: c.dir, log: filepath.Join(state, "gateway.log"),
		env: append(append([]string(nil), c.env...), "OPENCLAW_CONFIG_PATH="+file, "OPENCLAW_STATE_DIR="+state,
			"OPENCLAW_SKIP_CHANNELS=1", "OPENCLAW_SKIP_GMAIL_WATCHER=1", "OPENCLAW_SKIP_CRON=1", "OPENCLAW_SKIP_BROWSER_CONTROL_SERVER=1", "OPENCLAW_SKIP_CANVAS_HOST=1")}
	t.Cleanup(g.stop)
	return g
}

func (g *gateway) cli(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.binary, args...)
	cmd.Dir, cmd.Env = g.dir, g.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("isolated OpenClaw %s: %v\n%s", args[0], err, stderr.Bytes())
	}
	return out
}

func (g *gateway) start(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(g.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	g.cmd = exec.Command(g.binary, "gateway", "--port", g.port, "--bind", "loopback", "--allow-unconfigured")
	g.cmd.Dir, g.cmd.Env, g.cmd.Stdout, g.cmd.Stderr = g.dir, g.env, f, f
	err = g.cmd.Start()
	f.Close()
	if err != nil {
		g.cmd = nil
		t.Fatal(err)
	}
	g.done = make(chan error, 1)
	go func(cmd *exec.Cmd, done chan error) { done <- cmd.Wait() }(g.cmd, g.done)
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	cl := http.Client{Timeout: time.Second}
	for {
		select {
		case err := <-g.done:
			g.cmd = nil
			data, _ := os.ReadFile(g.log)
			if len(data) > 2500 {
				data = data[len(data)-2500:]
			}
			t.Fatalf("isolated gateway exited before ready: %v\n%s", err, data)
		case <-timeout.C:
			t.Fatal("isolated gateway did not become ready in 90 seconds")
		case <-tick.C:
			res, err := cl.Get("http://127.0.0.1:" + g.port + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					return
				}
			}
		}
	}
}

func (g *gateway) stop() {
	if g.cmd == nil {
		return
	}
	_ = g.cmd.Process.Signal(os.Interrupt)
	select {
	case <-g.done:
	case <-time.After(8 * time.Second):
		_ = g.cmd.Process.Kill()
		<-g.done
	}
	g.cmd = nil
}

func (g *gateway) proxy(t *testing.T, person, clientIP string, dropRelease *atomic.Bool) *httptest.Server {
	t.Helper()
	origin := &url.URL{Scheme: "http", Host: "127.0.0.1:" + g.port}
	p := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(origin)
		r.Out.Header.Set("X-Forwarded-User", person)
		r.Out.Header.Set("X-Forwarded-Proto", "http")
		r.Out.Header.Set("X-Forwarded-For", clientIP)
	}}
	if dropRelease != nil {
		p.ModifyResponse = func(r *http.Response) error {
			if r.StatusCode/100 == 2 && r.Request.Method == "POST" && strings.HasSuffix(r.Request.URL.Path, "/release") && dropRelease.CompareAndSwap(true, false) {
				_, _ = io.Copy(io.Discard, r.Body)
				return errors.New("fixture dropped committed release acknowledgement")
			}
			return nil
		}
	}
	server := httptest.NewServer(p)
	t.Cleanup(server.Close)
	return server
}

func (g *gateway) enableBeam(t *testing.T) {
	t.Helper()
	var installed struct {
		Plugin struct {
			ID, Origin string
			Enabled    *bool
		}
	}
	if err := json.Unmarshal(g.cli(t, "plugins", "inspect", "beam", "--json"), &installed); err != nil {
		t.Fatal(err)
	}
	if installed.Plugin.ID != "beam" || installed.Plugin.Origin != "bundled" || installed.Plugin.Enabled == nil || *installed.Plugin.Enabled {
		t.Fatalf("unexpected default Beam plugin installation: %+v", installed.Plugin)
	}
	g.cli(t, "plugins", "enable", "beam")
	var doctor struct{ OK bool }
	if err := json.Unmarshal(g.cli(t, "beam", "doctor", "--json"), &doctor); err != nil || !doctor.OK {
		t.Fatalf("installed Beam cleaner failed its doctor check: %v", err)
	}
}
