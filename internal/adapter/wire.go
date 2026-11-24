package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/openclaw/beam/internal/packet"
)

func ReadBounded(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, Fail("too_large", "never", fmt.Sprintf("input exceeds %d bytes", n))
	}
	return b, nil
}

func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}

var fields = map[string][]string{
	"capabilities":    {"op"},
	"list":            {"op", "cursor", "limit"},
	"read":            {"op", "cursor", "selection", "limit"},
	"stageReference":  {"op", "operationId", "packet"},
	"openCopy":        {"op", "operationId", "packet"},
	"lookupOperation": {"op", "operationId"},
	"resolve":         {"op", "handle"},
	"send":            {"op", "handle", "requestId", "instruction"},
}

type wireRequest struct {
	Op          string          `json:"op"`
	Cursor      string          `json:"cursor,omitempty"`
	Selection   string          `json:"selection,omitempty"`
	Limit       int             `json:"limit,omitempty"`
	OperationID string          `json:"operationId,omitempty"`
	Packet      json.RawMessage `json:"packet,omitempty"`
	Handle      string          `json:"handle,omitempty"`
	RequestID   string          `json:"requestId,omitempty"`
	Instruction string          `json:"instruction,omitempty"`
}

type requestEnvelope struct {
	Protocol int             `json:"protocol"`
	Request  json.RawMessage `json:"request"`
}

func EncodeRequest(r Request) ([]byte, error) {
	w := wireRequest{Op: r.Op, Cursor: r.Cursor, Selection: r.Selection, Limit: r.Limit, OperationID: r.OperationID, Handle: r.Handle, RequestID: r.RequestID, Instruction: r.Instruction}
	if r.Op == "openCopy" || r.Op == "stageReference" {
		b, err := packet.Marshal(r.Packet)
		if err != nil {
			return nil, err
		}
		w.Packet = b
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return json.Marshal(requestEnvelope{Protocol: Protocol, Request: raw})
}

func DecodeRequest(data []byte) (Request, error) {
	if err := checkWire(data); err != nil {
		return Request{}, err
	}
	if _, err := members(data, "protocol", "request"); err != nil {
		return Request{}, err
	}
	var env requestEnvelope
	if err := decode(data, &env); err != nil {
		return Request{}, err
	}
	if env.Protocol != Protocol {
		return Request{}, Fail("unsupported_version", "never", "unsupported adapter protocol")
	}
	var w wireRequest
	if err := decode(env.Request, &w); err != nil {
		return Request{}, err
	}
	allowed, ok := fields[w.Op]
	if !ok {
		return Request{}, Unsupported(w.Op)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(env.Request, &keys); err != nil {
		return Request{}, err
	}
	for k := range keys {
		if !slices.Contains(allowed, k) {
			return Request{}, fmt.Errorf("unexpected %q for %s", k, w.Op)
		}
	}
	r := Request{Op: w.Op, Cursor: w.Cursor, Selection: w.Selection, Limit: w.Limit, OperationID: w.OperationID, Handle: w.Handle, RequestID: w.RequestID, Instruction: w.Instruction}
	switch w.Op {
	case "list", "read":
		if r.Limit < 1 || r.Limit > 200 {
			return r, fmt.Errorf("limit must be between 1 and 200")
		}
		if w.Op == "read" && w.Selection == "" {
			return r, fmt.Errorf("selection is required")
		}
	case "openCopy", "stageReference":
		if w.OperationID == "" {
			return r, fmt.Errorf("operationId is required")
		}
		p, err := packet.Parse(w.Packet)
		if err != nil {
			return r, err
		}
		r.Packet = p
	case "lookupOperation":
		if w.OperationID == "" {
			return r, fmt.Errorf("operationId is required")
		}
	case "resolve":
		if w.Handle == "" {
			return r, fmt.Errorf("handle is required")
		}
	case "send":
		if w.Handle == "" || w.RequestID == "" || w.Instruction == "" {
			return r, fmt.Errorf("handle, requestId and instruction are required")
		}
	}
	return r, nil
}

// Serve handles exactly one process request. Adapter failures still have a machine-readable receipt.
func Serve(ctx context.Context, a Adapter, in io.Reader, out io.Writer) (int, error) {
	data, err := ReadBounded(in, MaxResponseBytes)
	var env Envelope
	if err == nil {
		var req Request
		req, err = DecodeRequest(data)
		if err == nil {
			var result any
			result, err = a.Call(ctx, req)
			if err == nil {
				env, err = Success(result)
			}
		}
	}
	if err != nil {
		env = Failure(err)
	}
	var b []byte
	b, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		return 2, marshalErr
	}
	if len(b) >= MaxResponseBytes {
		err = Fail("too_large", "never", "adapter result exceeds 4 MiB")
		b, _ = json.Marshal(Failure(err))
	}
	_, writeErr := out.Write(append(b, '\n'))
	return ExitCode(err), writeErr
}

func DecodeResult(data []byte, op string) (any, error) {
	if err := checkWire(data); err != nil {
		return nil, err
	}
	keys, err := members(data, "protocol", "ok", "result", "error")
	if err != nil {
		return nil, err
	}
	if string(keys["ok"]) != "true" && string(keys["ok"]) != "false" {
		return nil, fmt.Errorf("machine receipt requires boolean ok")
	}
	var env Envelope
	if err := decode(data, &env); err != nil {
		return nil, fmt.Errorf("invalid adapter response: %w", err)
	}
	if env.Protocol != Protocol {
		return nil, Fail("unsupported_version", "never", "unsupported adapter response protocol")
	}
	if !env.OK {
		e, err := typed[Error](keys["error"])
		if err != nil || e.Code == "" || e.Message == "" || !slices.Contains([]string{"never", "same_request", "after_reconcile"}, e.Retry) || len(env.Result) > 0 {
			return nil, fmt.Errorf("invalid adapter error envelope")
		}
		return nil, &e
	}
	if len(env.Result) == 0 || keys["error"] != nil {
		return nil, fmt.Errorf("invalid adapter success envelope")
	}
	switch op {
	case "capabilities":
		return typed[Capabilities](env.Result)
	case "list":
		return typed[ListResult](env.Result)
	case "read":
		return typed[ReadResult](env.Result)
	case "openCopy", "stageReference", "resolve", "lookupOperation":
		value, err := typed[OpenResult](env.Result)
		if err == nil && !validOpenResult(value, op) {
			err = fmt.Errorf("invalid %s receipt", op)
		}
		return value, err
	case "send":
		value, err := typed[SendResult](env.Result)
		if err == nil && (value.State != "accepted" && value.State != "completed" || value.RequestID == "" || value.Handle == "") {
			err = fmt.Errorf("invalid send receipt")
		}
		return value, err
	default:
		return nil, Unsupported(op)
	}
}
func typed[T any](b []byte) (T, error) {
	var v T
	err := packet.DecodeClosed(b, MaxResponseBytes, &v)
	return v, err
}

func validOpenResult(v OpenResult, op string) bool {
	switch v.State {
	case "ready":
		return v.Handle != "" && v.NativeSession != ""
	case "staged":
		return v.Handle != "" && v.NextAction != ""
	case "manual":
		return v.Artifact != "" && v.NextAction != ""
	case "needs_reconciliation":
		return v.NextAction != "" && (v.OperationID != "" || v.Handle != "")
	case "pending", "unknown":
		return op == "lookupOperation"
	default:
		return false
	}
}
