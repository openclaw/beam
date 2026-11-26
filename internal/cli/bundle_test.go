package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/bundle"
	"github.com/openclaw/beam/internal/packet"
)

func runBundleCLI(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	code, out, stderr := invoke(t, App{}, input, args...)
	if code != 0 {
		t.Fatalf("%v: %s %s", args, stderr, out)
	}
	return out
}

func TestBundleStripPNGMetadata(t *testing.T) {
	dir := t.TempDir()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewGray(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	source := append(bytes.Clone(imageBytes.Bytes()), "unreviewed trailer"...)
	path := filepath.Join(dir, "selected.image")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("exact notes"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := runBundleCLI(t, "message", "pack", "--from", "file", "--session", "-")
	for _, strip := range []bool{false, true} {
		args := []string{"bundle", "create", "-", "--attach", path, "--attach", note, "--out", "-"}
		expected := source
		if strip {
			args = append(args, "--strip-png-metadata")
			expected = imageBytes.Bytes()
		}
		z := runBundleCLI(t, string(raw), args...)
		b, err := bundle.Read(z)
		if err != nil {
			t.Fatal(err)
		}
		assets := b.Manifest.Assets
		if !bytes.Equal(b.PacketBytes, raw) || len(assets) != 2 || assets[0].Name != "selected.image" || assets[0].Digest != packet.Digest(expected) || assets[1].Digest != packet.Digest([]byte("exact notes")) {
			t.Fatal("wrong reviewed bundle contents")
		}
		page := runBundleCLI(t, string(z), "bundle", "preview", "-", "--out", "-")
		if !strings.Contains(html.UnescapeString(string(page)), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(expected)) {
			t.Fatal("preview disagrees with reviewed bytes")
		}
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, source) {
		t.Fatal("source modified")
	}
	if err := os.WriteFile(path, source[:20], 0600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "existing.zip")
	if err := os.WriteFile(dst, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := invoke(t, App{}, string(raw), "bundle", "create", "-", "--attach", path, "--strip-png-metadata", "--out", dst)
	if got, err := os.ReadFile(dst); code == 0 || len(out) != 0 || err != nil || string(got) != "existing" {
		t.Fatal("failed cleaning replaced output")
	}
}

func TestBundleCLI(t *testing.T) {
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	a := App{}

	raw := runBundleCLI(t, "message", "pack", "--from", "file", "--session", "-")
	for _, version := range []string{"1", "2"} {
		t.Run(version, func(t *testing.T) {
			selected := runBundleCLI(t, string(raw), "convert", "-", "--packet-version", version)
			z := runBundleCLI(t, string(selected), "bundle", "create", "-", "--attach", note, "--out", "-", "--expect-digest", packet.Digest(selected))
			out := runBundleCLI(t, string(z), "bundle", "inspect", "-", "--json", "--expect-digest", packet.Digest(z))
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
			page := runBundleCLI(t, string(z), "bundle", "preview", "-", "--out", "-")
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
