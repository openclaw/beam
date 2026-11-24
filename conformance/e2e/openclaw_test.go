//go:build beam_openclaw

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenClawPublishReleaseRestart(t *testing.T) {
	c := newCLI(t)
	models := new(modelCalls)
	model := httptest.NewServer(models)
	defer model.Close()
	g := newGateway(t, c, model.URL)
	g.enableBeam(t)
	g.start(t)
	var receipts gatewayReceipts
	a := g.proxy(t, "publisher@example.test", "192.0.2.10", &receipts)
	b := g.proxy(t, "teammate@example.test", "192.0.2.11", nil)
	packet := c.reviewed(t)
	file := filepath.Join(c.dir, "reviewed.beam.json")
	if err := os.WriteFile(file, packet, 0600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(c.dir, "publisher.sqlite")
	var pub struct {
		PublicationID, APIURL, Revision, Digest string
		CanRelease                              bool
	}
	machineResult(t, c.run(t, nil, "share", file, "--to", a.URL, "--state", db, "--json"), &pub)
	if !pub.CanRelease || pub.PublicationID == "" || pub.Revision == "" || pub.Digest == "" {
		t.Fatalf("publisher receipt missing publication authority: %+v", pub)
	}
	link, err := url.Parse(pub.APIURL)
	if err != nil {
		t.Fatal(err)
	}
	machinePath := link.EscapedPath()
	// Staff can share access to this synthetic publication, but only the
	// verified original publisher may release it.
	body, _ := json.Marshal(map[string]string{"digest": pub.Digest})
	request, err := http.NewRequestWithContext(t.Context(), "POST", b.URL+machinePath+"/release", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("If-Match", pub.Revision)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "10000000-0000-4000-8000-000000000001")
	res, err := b.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusNotFound {
		t.Fatalf("second person's release returned %d", res.StatusCode)
	}
	receipts.dropRelease.Store(true)
	_, _, _ = c.command(t, nil, "handoff", file, "--to", a.URL, "--state", db, "--json")
	if receipts.dropRelease.Load() {
		t.Fatal("did not reach a committed release response")
	}
	c.run(t, nil, "handoff", file, "--to", a.URL, "--state", db, "--json")
	other := filepath.Join(c.dir, "recipient.sqlite")
	pulled := filepath.Join(c.dir, "pulled.beam.json")
	c.run(t, nil, "pull", b.URL+machinePath, "--endpoint", b.URL, "--state", other, "--out", pulled)
	got, err := os.ReadFile(pulled)
	if err != nil || !bytes.Equal(got, packet) {
		t.Fatalf("new recipient did not receive the reviewed bytes: %v", err)
	}
	var dest struct{ State, Reference string }
	resolve := func() {
		machineResult(t, c.run(t, nil, "resolve", b.URL+machinePath, "--endpoint", b.URL, "--state", other, "--json"), &dest)
	}
	resolve()
	if dest.State != "released_unbound" || len(models.seen()) != 0 {
		t.Fatalf("release or read created a turn or destination: %+v", dest)
	}
	const requestID = "10000000-0000-4000-8000-000000000002"
	args := []string{"continue", a.URL + machinePath, "--endpoint", a.URL, "--state", db, "--message", "Answer the synthetic fixture question", "--request-id", requestID, "--json"}
	receipts.dropContinuation.Store(true)
	if _, _, err := c.command(t, nil, args...); err == nil || receipts.dropContinuation.Load() {
		t.Fatal("expected an unreadable outward receipt after the Gateway accepted the continuation")
	}
	for range 2 {
		var receipt struct{ RequestID, State string }
		machineResult(t, c.run(t, nil, args...), &receipt)
		if receipt.RequestID != requestID || (receipt.State != "accepted" && receipt.State != "completed") {
			t.Fatalf("same-UUID recovery returned an unexpected receipt: %+v", receipt)
		}
	}
	if receipts.sendPosts.Load() != 1 || receipts.statusReads.Load() != 2 {
		t.Fatalf("expected one continuation POST and two receipt GETs, got %d and %d", receipts.sendPosts.Load(), receipts.statusReads.Load())
	}
	wait, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for len(models.seen()) == 0 && wait.Err() == nil {
		select {
		case <-wait.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	requests := models.seen()
	if len(requests) != 1 || !bytes.Contains(requests[0], []byte(sourceMarker)) {
		t.Fatalf("replayed instruction produced %d model requests or lost source context", len(requests))
	}
	resolve()
	if dest.State != "ready" || dest.Reference == "" {
		t.Fatalf("destination receipt: %+v", dest)
	}
	ref := dest.Reference
	g.stop()
	g.start(t)
	resolve()
	if dest.State != "ready" || dest.Reference != ref || len(models.seen()) != 1 {
		t.Fatalf("restart changed bound destination or resent the instruction: %+v", dest)
	}
}
