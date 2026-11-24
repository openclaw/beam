package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

// Adapter implements the same one-request contract for in-process and external harnesses.
type Adapter interface {
	Call(context.Context, Request) (any, error)
}

const Protocol = 1
const MaxResponseBytes = 4 << 20

type Request struct {
	Op          string
	Cursor      string
	Selection   string
	OperationID string
	Handle      string
	RequestID   string
	Instruction string
	Limit       int
	Packet      packet.Packet
}

type Capabilities struct {
	Protocol    int      `json:"protocol"`
	Adapter     string   `json:"adapter"`
	Version     string   `json:"version"`
	Source      []string `json:"source"`
	Destination []string `json:"destination"`
	Recovery    string   `json:"recovery"`
}

type Entry struct {
	Selection string `json:"selection"`
	Title     string `json:"title"`
}

type ListResult struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"nextCursor,omitempty"`
}

type ReadResult struct {
	clean.Candidate
	NextCursor string `json:"nextCursor,omitempty"`
}

type OpenResult struct {
	State         string `json:"state"`
	Handle        string `json:"handle,omitempty"`
	NativeSession string `json:"nativeSession,omitempty"`
	NextAction    string `json:"nextAction,omitempty"`
	Artifact      string `json:"artifact,omitempty"`
	OperationID   string `json:"operationId,omitempty"`
}

type SendResult struct {
	State     string `json:"state"`
	RequestID string `json:"requestId"`
	Handle    string `json:"handle"`
}

type Error struct {
	Code    string `json:"code"`
	Retry   string `json:"retry"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func Fail(code, retry, message string) error {
	return &Error{Code: code, Retry: retry, Message: message}
}

func Describe(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: "invalid_input", Retry: "never", Message: err.Error()}
}

type Envelope struct {
	Protocol int             `json:"protocol"`
	OK       bool            `json:"ok"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Error          `json:"error,omitempty"`
}

func Success(value any) (Envelope, error) {
	// Empty pages are successful reads. Encode their arrays consistently even
	// when a harness returned a Go zero-value slice.
	switch r := value.(type) {
	case ListResult:
		if r.Entries == nil {
			r.Entries = []Entry{}
			value = r
		}
	case ReadResult:
		if r.Items == nil {
			r.Items = []clean.Item{}
			value = r
		}
	}
	b, err := json.Marshal(value)
	return Envelope{Protocol: Protocol, OK: true, Result: b}, err
}

func Failure(err error) Envelope {
	return Envelope{Protocol: Protocol, Error: Describe(err)}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch Describe(err).Code {
	case "unsupported", "unavailable", "not_installed", "unsupported_version", "unsupported_operation", "unsupported_capability":
		return 3
	case "authentication_required", "unauthorized", "forbidden":
		return 4
	case "conflict", "needs_reconciliation", "request_identity_conflict", "source_changed", "revision_changed":
		return 5
	case "temporary", "rate_limited", "timeout", "transport_failure":
		return 6
	default:
		return 2
	}
}

func Unsupported(op string) error {
	return Fail("unsupported_operation", "never", fmt.Sprintf("adapter does not support %q", op))
}
