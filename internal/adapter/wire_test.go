package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type adapterFunc func(context.Context, Request) (any, error)

func (f adapterFunc) Call(c context.Context, r Request) (any, error) { return f(c, r) }

func TestClosedRequest(t *testing.T) {
	input := []string{
		`{"protocol":1,"protocol":1,"request":{"op":"capabilities"}}`,
		`{"Protocol":1,"request":{"op":"capabilities"}}`,
		`{"protocol":1,"request":{"op":"capabilities","handle":"extra"}}`,
		`{"protocol":3,"request":{"op":"capabilities"}}`,
		`{"protocol":1,"request":{"op":"read","selection":"a","limit":201}}`,
		`{"protocol":1,"request":{"op":"openCopy","operationId":"x","packet":{"version":2}}}`,
	}
	for _, s := range input {
		if _, err := DecodeRequest([]byte(s)); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
func TestMachineReadAndErrors(t *testing.T) {
	data, err := EncodeRequest(Request{Op: "list", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	a := adapterFunc(func(ctx context.Context, r Request) (any, error) {
		return ListResult{Entries: []Entry{{Selection: "x", Title: "first"}}}, nil
	})
	code, err := Serve(context.Background(), a, bytes.NewReader(data), &out)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	result, err := DecodeResult(out.Bytes(), "list")
	if err != nil || result.(ListResult).Entries[0].Selection != "x" {
		t.Fatal(result, err)
	}
}
func TestMain(m *testing.M) {
	switch os.Getenv("BEAM_TEST_EXTERNAL_MODE") {
	case "bad":
		fmt.Print("unstructured diagnostic")
		os.Exit(1)
	case "large":
		fmt.Print(strings.Repeat("x", MaxResponseBytes+100))
		os.Exit(0)
	case "timeout":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "receipt":
		fmt.Print(`{"protocol":1,"ok":true,"result":{"entries":[]}}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestExternalBoundary(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ mode, op, code string }{{"receipt", "list", ""}, {"large", "list", "too_large"}, {"bad", "send", "needs_reconciliation"}, {"timeout", "list", "timeout"}} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("BEAM_TEST_EXTERNAL_MODE", tc.mode)
			a := External{Path: path, Timeout: 5 * time.Second}
			if tc.mode == "timeout" {
				a.Timeout = 100 * time.Millisecond
			}
			_, err := a.Call(context.Background(), Request{Op: tc.op, Limit: 1})
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || Describe(err).Code != tc.code {
				t.Fatalf("got %v, expected %s", err, tc.code)
			}
		})
	}
}
