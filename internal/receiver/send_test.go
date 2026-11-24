package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openclaw/beam/internal/journal"
	"github.com/openclaw/beam/internal/packet"
)

func TestLostSendAcknowledgementUsesStatusAndNoPlaintextJournal(t *testing.T) {
	const instruction = "a private new instruction"
	const id = "4915ca60-754f-4ac2-afc3-5f269af52053"
	link := v2Path + "/publications/gen"
	var writes, queries atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case v2Path + "/capabilities":
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["continue"],"maxBodyBytes":57344,"sendReceiptRetentionSeconds":3600}`)
		case link + "/continue":
			if r.Method != "POST" || r.Header.Get("Idempotency-Key") != id {
				t.Error("unkeyed user send")
			}
			var input Instruction
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Text != instruction || input.RequestID != id {
				t.Error("altered input")
			}
			writes.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
		case link + "/requests/" + id:
			queries.Add(1)
			json.NewEncoder(w).Encode(SendReceipt{RequestID: id, State: "accepted", Destination: &Destination{State: "ready", Reference: "opaque"}})
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	state := filepath.Join(t.TempDir(), "state.sqlite")
	c := clientFor(t, srv.URL, state)
	ctx := context.Background()
	user := Instruction{RequestID: id, Text: instruction}
	if _, err := c.Continue(ctx, link, user); err == nil {
		t.Fatal("lost response appeared successful")
	}
	c.Close()
	raw, err := os.ReadFile(state)
	if err != nil || bytes.Contains(raw, []byte(instruction)) {
		t.Fatal("journal retained private user instruction", err)
	}
	c = clientFor(t, srv.URL, state)
	out, err := c.Continue(ctx, link, user)
	if err != nil || out.State != "accepted" {
		t.Fatalf("reconcile: %+v %v", out, err)
	}
	if _, err := c.Continue(ctx, link, user); err != nil {
		t.Fatal(err)
	}
	user.Text = "changed"
	if _, err := c.Continue(ctx, link, user); err == nil {
		t.Fatal("reused operation for changed intent")
	}
	if writes.Load() != 1 || queries.Load() != 2 {
		t.Fatalf("writes=%d queries=%d", writes.Load(), queries.Load())
	}
}

func TestNeverReplayExpiredUnknownSend(t *testing.T) {
	var writes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/capabilities"):
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["continue"],"maxBodyBytes":57344,"sendReceiptRetentionSeconds":60}`)
		case strings.Contains(r.URL.Path, "/requests/"):
			w.WriteHeader(404)
		default:
			writes.Add(1)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := clientFor(t, srv.URL, filepath.Join(t.TempDir(), "state.sqlite"))
	st, err := c.journal()
	if err != nil {
		t.Fatal(err)
	}
	input := Instruction{RequestID: "ad6d7344-063d-497c-8811-245654065557", Text: "go"}
	b, _ := json.Marshal(input)
	if _, err := st.Begin(ctx, journal.Pending{Endpoint: c.legacyEndpoint, Kind: "send", Resource: "gen", ID: input.RequestID, Body: []byte(packet.Digest(b)), Created: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Continue(ctx, v2Path+"/publications/gen", input); err == nil || !strings.Contains(err.Error(), "needs_reconciliation") {
		t.Fatal("resubmitted unknown turn", err)
	}
	if writes.Load() != 0 {
		t.Fatal("write after send-receipt expiry")
	}
}

func TestConditionalReleaseRefusalRestoresOnlyVerifiedWritableSource(t *testing.T) {
	ctx := context.Background()
	data := fixture(t)
	const gen = "same-generation"
	var headReads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pub := Publication{BeamID: fixtureID, PublicationID: gen, Revision: "\"first\"", Digest: packet.Digest(data), URL: "/beam/0123456789ab", APIURL: v2Path + "/publications/" + gen, CanRelease: true, State: "writable"}
		switch {
		case strings.HasSuffix(r.URL.Path, "/capabilities"):
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["publish","release"],"maxBodyBytes":57344}`)
		case strings.HasSuffix(r.URL.Path, "/head"):
			headReads++
			if headReads == 2 {
				pub.Revision = "\"new\""
			}
			json.NewEncoder(w).Encode(pub)
		case strings.HasSuffix(r.URL.Path, "/release"):
			w.WriteHeader(412)
		default:
			t.Error(r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	c := clientFor(t, srv.URL, filepath.Join(t.TempDir(), "state.sqlite"))
	if _, err := c.Handoff(ctx, fixtureID, data); !asHTTP(err, 412) {
		t.Fatal(err)
	}
	if _, found, err := c.LegacyRow(ctx, fixtureID); err != nil || found {
		t.Fatalf("definitely refused writable source stayed fenced: found=%v err=%v", found, err)
	}
}
