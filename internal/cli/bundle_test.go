package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func TestBundleCLI(t *testing.T) {
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	a := App{}
	run := func(input string, args ...string) []byte {
		t.Helper()
		code, out, stderr := invoke(t, a, input, args...)
		if code != 0 {
			t.Fatalf("%v: %s %s", args, stderr, out)
		}
		return out
	}
	raw := run("message", "pack", "--from", "file", "--session", "-")
	for _, version := range []string{"1", "2"} {
		t.Run(version, func(t *testing.T) {
			selected := run(string(raw), "convert", "-", "--packet-version", version)
			z := run(string(selected), "bundle", "create", "-", "--attach", note, "--out", "-", "--expect-digest", packet.Digest(selected))
			out := run(string(z), "bundle", "inspect", "-", "--json", "--expect-digest", packet.Digest(z))
			var reply struct {
				OK     bool
				Result struct {
					BundleDigest string
					Manifest     struct{ PacketDigest string }
				}
			}
			if json.Unmarshal(out, &reply) != nil || !reply.OK || reply.Result.BundleDigest != packet.Digest(z) || reply.Result.Manifest.PacketDigest != packet.Digest(selected) {
				t.Fatal(string(out))
			}
			page := run(string(z), "bundle", "preview", "-", "--out", "-")
			if !bytes.Contains(page, []byte("note.txt")) {
				t.Fatal("preview missing asset")
			}
		})
	}
	code, _, _ := invoke(t, a, string(raw), "bundle", "create", "-", "--attach", note, "--out", note)
	got, err := os.ReadFile(note)
	if code == 0 || err != nil || string(got) != "notes" {
		t.Fatal("input overwritten")
	}
	dst := filepath.Join(dir, "bundle.zip")
	code, _, _ = invoke(t, a, string(raw), "bundle", "create", "-", "--attach", note, "--attach", filepath.Join(dir, "missing"), "--out", dst)
	if _, err := os.Stat(dst); code == 0 || !os.IsNotExist(err) {
		t.Fatal("failed creation left a partial artifact")
	}
}
func TestBundleRedirectedInputPreserved(t *testing.T) {
	a := App{}
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	code, raw, _ := invoke(t, a, "message", "pack", "--from", "file", "--session", "-")
	if code != 0 {
		t.Fatal("pack failed")
	}
	code, blob, _ := invoke(t, a, string(raw), "bundle", "create", "-", "--attach", note, "--out", "-")
	if code != 0 {
		t.Fatal("bundle creation failed")
	}
	for _, tc := range []struct {
		cmd  string
		data []byte
	}{{"create", raw}, {"preview", blob}} {
		t.Run(tc.cmd, func(t *testing.T) {
			name := filepath.Join(dir, tc.cmd)
			if err := os.WriteFile(name, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			in, err := os.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			args := []string{"bundle", tc.cmd, "-", "--out", name}
			if tc.cmd == "create" {
				args = append(args, "--attach", note)
			}
			if code := a.Run(t.Context(), args, in, io.Discard, io.Discard); code == 0 {
				t.Fatal("accepted input as output")
			}
			got, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(got, tc.data) {
				t.Fatal("redirected input changed")
			}
		})
	}
}
