package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func TestPrefixedGatewayReadsUnboundRowAndAdoptsConditionally(t *testing.T) {
	data := fixture(t)
	const mount = "/team"
	const revision = `"r1"`
	const gen = "publication"
	digest := packet.Digest(data)
	snapshot := mount + v2Path + "/snapshots/" + fixtureID
	human := mount + "/console/beam/" + fixtureID
	var adopted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mount + v2Path + "/capabilities":
			fmt.Fprint(w, `{"protocol":2,"packetVersions":[2],"operations":["read","publish","release"],"maxBodyBytes":57344}`)
		case snapshot + "/head":
			head := Publication{BeamID: fixtureID, Revision: revision, Digest: digest, URL: "../../../../../console/beam/" + fixtureID}
			if adopted.Load() {
				head.State = "writable"
				head.CanRelease = true
				head.PublicationID = gen
				head.APIURL = "../../publications/" + gen
			} else {
				head.State = "legacy_unbound"
				head.SnapshotURL = "snapshot"
			}
			json.NewEncoder(w).Encode(head)
		case snapshot + "/snapshot":
			if r.Header.Get("If-Match") != revision || adopted.Load() {
				w.WriteHeader(412)
				return
			}
			w.Header().Set("ETag", revision)
			w.Header().Set("Beam-Digest", digest)
			w.Write(data)
		case snapshot:
			if r.Method != "PUT" || r.Header.Get("If-Match") != revision {
				t.Error("unguarded adoption")
			}
			adopted.Store(true)
			json.NewEncoder(w).Encode(Publication{BeamID: fixtureID, PublicationID: gen, Revision: revision, Digest: digest, URL: "../../../../console/beam/" + fixtureID, APIURL: "../publications/" + gen, Disposition: "applied", CanRelease: true, State: "writable"})
		case mount + v2Path + "/publications/" + gen + "/release":
			json.NewEncoder(w).Encode(Release{PublicationID: gen, Revision: revision, State: "released", URL: "../../../../../console/beam/" + fixtureID})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	state := filepath.Join(t.TempDir(), "state.sqlite")
	var out, stderr bytes.Buffer
	for _, key := range []string{"BEAM_AUTH_TOKEN", "BEAM_ACCESS_TOKEN", "CF_ACCESS_CLIENT_ID", "CF_ACCESS_CLIENT_SECRET"} {
		t.Setenv(key, "")
	}
	code, err := Run(ctx, "pull", []string{srv.URL + human, "--endpoint", srv.URL + mount, "--state", state}, strings.NewReader(""), &out, &stderr)
	if err != nil || code != 0 || !bytes.Equal(out.Bytes(), data) || adopted.Load() {
		t.Fatalf("pull returned %d %v %s", code, err, stderr.String())
	}
	c := clientFor(t, srv.URL+mount, state)
	pub, err := c.Share(ctx, data)
	if err != nil || !adopted.Load() || pub.URL != srv.URL+human || pub.APIURL != srv.URL+mount+v2Path+"/publications/"+gen {
		t.Fatalf("adoption: %+v %v", pub, err)
	}
	rel, err := c.Handoff(ctx, fixtureID, data)
	if err != nil || rel.URL != pub.URL || rel.APIURL != pub.APIURL {
		t.Fatalf("release: %+v %v", rel, err)
	}
}

func TestRejectUnsafeRelativeReceiptReferences(t *testing.T) {
	c, err := New(Config{Endpoint: "https://gateway.example/team"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"//other.example/a", "https://other.example/a", "../../../../../../outside", "%2e%2e/elsewhere", "../some?secret=hidden", "snapshot#fragment", "https://user@gateway.example/team/secret"} {
		if _, err := c.responseLink(v2Path+"/snapshots/"+fixtureID+"/head", raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
