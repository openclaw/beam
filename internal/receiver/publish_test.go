package receiver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openclaw/beam/internal/packet"
)

func publishPeer(t *testing.T, put http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == v2Path+"/capabilities":
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["publish"],"maxBodyBytes":57344}`)
		case strings.HasSuffix(r.URL.Path, "/head"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == v2Path+"/snapshots/"+fixtureID:
			put(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func acceptPublish(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	json.NewEncoder(w).Encode(Publication{
		BeamID: fixtureID, PublicationID: "one", Revision: `"1"`, State: "writable",
		Digest: packet.Digest(body), Disposition: "applied", CanRelease: true,
		URL: "/beam/" + fixtureID, APIURL: v2Path + "/publications/one",
	})
}

func TestGoalPacketRequiresAdvertisedVersionBeforePublishing(t *testing.T) {
	srv := publishPeer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("published unsupported goal packet") })
	p, err := packet.Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	p.Version, p.Goal = 3, &packet.Goal{Objective: "repair", Status: "paused"}
	raw, err := packet.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "journal.sqlite")
	c := clientFor(t, srv.URL, state)
	defer c.Close()
	if _, err := c.Share(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "unsupported_packet_version") {
		t.Fatal("unsupported version accepted", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("unsupported version wrote journal state", err)
	}
}

func TestIncompleteFirstPublishPreservesLegacyRetry(t *testing.T) {
	for _, status := range []int{200, 413} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			keys := make(chan string, 1)
			srv := publishPeer(t, func(w http.ResponseWriter, r *http.Request) {
				keys <- r.Header.Get("Idempotency-Key")
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(status)
				fmt.Fprint(w, "{")
			})
			state := filepath.Join(t.TempDir(), "journal.sqlite")
			body := fixture(t)
			client := clientFor(t, srv.URL, state)
			if _, err := client.Share(t.Context(), body); err == nil || !strings.Contains(err.Error(), "incomplete_response") {
				t.Fatal(err)
			}
			client.Close()
			db, err := sql.Open("sqlite", state)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var key, condition string
			var saved []byte
			err = db.QueryRow(`SELECT id,body,condition FROM beam_pending WHERE kind='publish' AND resource=?`, fixtureID).Scan(&key, &saved, &condition)
			if err != nil || key != <-keys || !bytes.Equal(saved, body) || condition != "*" {
				t.Fatal("earlier CLI cannot replay the exact uncertain request", err)
			}
		})
	}
}

func TestCorrectPublishAfterContentRefusal(t *testing.T) {
	for _, status := range []int{400, 413, 415} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var keys []string
			srv := publishPeer(t, func(w http.ResponseWriter, r *http.Request) {
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				if len(keys) == 1 {
					w.WriteHeader(status)
					return
				}
				acceptPublish(t, w, r)
			})
			state := filepath.Join(t.TempDir(), "journal.sqlite")
			first := clientFor(t, srv.URL, state)
			body := fixture(t)
			if _, err := first.Share(t.Context(), body); !asHTTP(err, status) {
				t.Fatal(err)
			}
			first.Close()
			restarted := clientFor(t, srv.URL, state)
			corrected := bytes.Replace(body, []byte("reviewed context"), []byte("short"), 1)
			if _, err := restarted.Share(t.Context(), corrected); err != nil {
				t.Fatal(err)
			}
			if len(keys) != 2 || keys[0] == keys[1] {
				t.Fatalf("expected two distinct write intents, got %v", keys)
			}
		})
	}
}

func TestContentRefusalWhileAnotherPublishMayHaveSucceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var writes atomic.Int32
	firstArrived, replayArrived := make(chan struct{}), make(chan struct{})
	finishReplay := make(chan struct{})
	var key string
	srv := publishPeer(t, func(w http.ResponseWriter, r *http.Request) {
		switch writes.Add(1) {
		case 1:
			key = r.Header.Get("Idempotency-Key")
			close(firstArrived)
			select {
			case <-replayArrived:
			case <-ctx.Done():
			}
			w.WriteHeader(413)
		case 2:
			if r.Header.Get("Idempotency-Key") != key {
				t.Error("concurrent publisher changed the request identity")
			}
			close(replayArrived)
			select {
			case <-finishReplay:
			case <-ctx.Done():
			}
			// The origin may have committed this PUT before the response broke.
			w.Header().Set("Content-Length", "1000")
			fmt.Fprint(w, "{")
		case 3:
			w.WriteHeader(413)
		case 4:
			if r.Header.Get("Idempotency-Key") != key {
				t.Error("restarted publisher changed the request identity")
			}
			acceptPublish(t, w, r)
		default:
			t.Error("unexpected publish attempt")
		}
	})
	body := fixture(t)
	corrected := bytes.Replace(body, []byte("reviewed context"), []byte("short"), 1)
	state := filepath.Join(t.TempDir(), "journal.sqlite")
	first, second := clientFor(t, srv.URL, state), clientFor(t, srv.URL, state)
	run := func(c *Client) <-chan error {
		done := make(chan error, 1)
		go func() { _, err := c.Share(ctx, body); done <- err }()
		return done
	}
	firstErr := run(first)
	select {
	case <-firstArrived:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondErr := run(second)
	if err := <-firstErr; !asHTTP(err, 413) {
		t.Fatal(err)
	}
	if _, err := first.Share(ctx, corrected); err == nil || !strings.Contains(err.Error(), "pending_publish_differs") {
		t.Fatalf("lost the in-flight intent: %v", err)
	}
	close(finishReplay)
	if err := <-secondErr; err == nil || !strings.Contains(err.Error(), "incomplete_response") {
		t.Fatal(err)
	}
	first.Close()
	second.Close()
	restarted := clientFor(t, srv.URL, state)
	if _, err := restarted.Share(ctx, body); !asHTTP(err, 413) {
		t.Fatal(err)
	}
	if _, err := restarted.Share(ctx, corrected); err == nil || !strings.Contains(err.Error(), "pending_publish_differs") {
		t.Fatalf("lost the uncertain intent: %v", err)
	}
	if _, err := restarted.Share(ctx, body); err != nil || writes.Load() != 4 {
		t.Fatalf("could not reconcile original publish: %v (%d writes)", err, writes.Load())
	}
}
