package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type server struct {
	cmd            *exec.Cmd
	base, password string
	http           *http.Client
	once           sync.Once
}

// Start a private loopback listener for this operation. A per-child password
// keeps another local process from taking over the session through this port.
func start(ctx context.Context) (*server, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "opencode", "serve", "--hostname", "127.0.0.1", "--port", "0")
	password := hex.EncodeToString(token[:])
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_USERNAME=beam", "OPENCODE_SERVER_PASSWORD="+password)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &server{cmd: cmd, password: password, http: &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	lines := bufio.NewScanner(stdout)
	for lines.Scan() {
		raw, found := strings.CutPrefix(lines.Text(), "opencode server listening on ")
		if !found {
			continue
		}
		addr, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || addr.Scheme != "http" || addr.Hostname() != "127.0.0.1" || addr.Port() == "" || addr.User != nil || addr.Path != "" {
			break
		}
		s.base = addr.String()
		// Drain remaining logs without exposing session or workspace content.
		go func() {
			for lines.Scan() {
			}
		}()
		return s, nil
	}
	s.close()
	return nil, errors.New("OpenCode server did not advertise a loopback listener")
}
func (s *server) close() { s.once.Do(func() { _ = s.cmd.Process.Kill(); _ = s.cmd.Wait() }) }

func (s *server) request(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var input bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&input).Encode(body); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, &input)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("beam", s.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("OpenCode rejected the local operation")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, errors.New("native OpenCode response exceeds 4 MiB")
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, err
		}
	}
	return resp.Header, nil
}
