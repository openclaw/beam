package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// The app-server uses newline-delimited JSON-RPC on stdio. There is only one
// outstanding request, but notifications and server requests can interleave.
type client struct {
	once sync.Once
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	id   int
}

func start(ctx context.Context, bin string) (*client, error) {
	cmd := exec.CommandContext(ctx, bin, "app-server")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := &client{cmd: cmd, in: in, out: bufio.NewScanner(stdout)}
	c.out.Buffer(make([]byte, 16384), 4<<20)
	err = c.call("initialize", obj{"clientInfo": obj{"name": "beam", "version": "1"}, "capabilities": obj{"experimentalApi": true}}, nil)
	if err == nil {
		err = json.NewEncoder(in).Encode(obj{"method": "initialized"})
	}
	if err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
func (c *client) close() {
	c.once.Do(func() {
		c.in.Close()
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})
}

type obj = map[string]any
type event struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int
	Message string
	method  string
}

func (e *rpcError) Error() string { return fmt.Sprintf("native request failed: %s", e.method) }

func (c *client) next() (event, error) {
	for c.out.Scan() {
		var ev event
		if err := json.Unmarshal(c.out.Bytes(), &ev); err != nil {
			return ev, err
		}
		if ev.Method != "" && len(ev.ID) > 0 {
			// Beam never grants approvals or supplies input on a person's behalf.
			if err := json.NewEncoder(c.in).Encode(obj{"id": ev.ID, "error": obj{"code": -32601, "message": "Interactive request requires the native client"}}); err != nil {
				return ev, err
			}
			continue
		}
		return ev, nil
	}
	if err := c.out.Err(); err != nil {
		return event{}, err
	}
	return event{}, io.EOF
}
func (c *client) call(method string, params any, out any) error {
	c.id++
	reqID, _ := json.Marshal(c.id)
	if err := json.NewEncoder(c.in).Encode(obj{"id": c.id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		ev, err := c.next()
		if err != nil {
			return err
		}
		if string(ev.ID) != string(reqID) {
			continue
		}
		if ev.Error != nil {
			ev.Error.method = method
			return ev.Error
		}
		if out != nil {
			return json.Unmarshal(ev.Result, out)
		}
		return nil
	}
}
