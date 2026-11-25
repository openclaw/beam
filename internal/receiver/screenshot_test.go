package receiver

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/beam/internal/bundle"
	"github.com/openclaw/beam/internal/packet"
)

func TestShareReviewedScreenshot(t *testing.T) {
	dir := t.TempDir()
	raw := append(fixture(t), '\n')
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "review.png")
	if err := os.WriteFile(file, pngBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	archive, _, err := bundle.Create(raw, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	var writes int
	var head *Publication
	supported := false
	loseReply := true
	var recordedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case v2Path + "/capabilities":
			ops := []string{"publish"}
			if supported {
				ops = append(ops, "publishScreenshot")
			}
			json.NewEncoder(w).Encode(Capabilities{Protocol: 2, PacketVersions: []int{2}, MaxBodyBytes: packet.MaxBytes, MaxScreenshotBytes: 512 << 10, Operations: ops})
		case v2Path + "/snapshots/" + fixtureID + "/head":
			if head == nil {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(head)
		case v2Path + "/snapshots/" + fixtureID:
			writes++
			if r.Method != "PUT" || r.Header.Get("Content-Type") != screenshotContentType || r.Header.Get("If-None-Match") != "*" {
				t.Errorf("unexpected rich PUT")
			}
			var envelope struct {
				Packet, Name string
				PNG          []byte
			}
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || envelope.Packet != string(raw) || envelope.Name != "review.png" || !bytes.Equal(envelope.PNG, pngBytes.Bytes()) {
				t.Errorf("changed screenshot or packet")
			}
			key := r.Header.Get("Idempotency-Key")
			if recordedKey != "" && recordedKey != key {
				t.Error("replay changed request id")
			}
			recordedKey = key
			head = &Publication{BeamID: fixtureID, PublicationID: "one", Revision: `"1"`, State: "writable", Digest: packet.Digest(raw), PresentationDigest: packet.Digest(append([]byte("review.png\x00"), pngBytes.Bytes()...)), Disposition: "applied", URL: "/beam/review", APIURL: v2Path + "/publications/one"}
			if loseReply {
				loseReply = false
				w.Header().Set("Content-Length", "100")
				w.Write([]byte("{"))
				return
			}
			json.NewEncoder(w).Encode(head)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	run := func() (int, error) {
		var out bytes.Buffer
		return Run(t.Context(), "share", []string{"-", "--to", srv.URL, "--state", filepath.Join(dir, "state.sqlite"), "--json"}, bytes.NewReader(archive), &out, &out)
	}
	if code, _ := run(); code != 3 || writes != 0 {
		t.Fatalf("unsupported receiver: exit=%d writes=%d", code, writes)
	}
	supported = true
	if code, _ := run(); code == 0 {
		t.Fatal("lost acknowledgement reported success")
	}
	if code, err := run(); err != nil || code != 0 || writes != 2 {
		t.Fatalf("retry: %d %v writes=%d", code, err, writes)
	}
	if code, err := run(); err != nil || code != 0 || writes != 2 {
		t.Fatalf("unchanged: %d %v writes=%d", code, err, writes)
	}
}
