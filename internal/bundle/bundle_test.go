package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openclaw/beam/internal/packet"
)

func TestBundleRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n') // A wire-equivalent reserialization would lose this reviewed byte.
	dir := t.TempDir()
	doc := filepath.Join(dir, "review & notes.pdf")
	data := []byte("%PDF-1.4\nreviewed bytes\n%%EOF")
	if err := os.WriteFile(doc, data, 0600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join("..", "..", "site", "beam-cover.webp")
	blob, m, err := Create(raw, []string{image, doc})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(blob)
	if err != nil {
		t.Fatal(err)
	}
	if m.PacketDigest != packet.Digest(raw) || m.Assets[0].MediaType != "image/webp" || m.Assets[1].Path != "assets/02.pdf" || !slices.Equal(b.Manifest.Assets, m.Assets) || !bytes.Equal(b.files[1], data) {
		t.Fatal("wrong bundle inventory")
	}
	z, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.Open(packetPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("packet bytes changed")
	}
	view, err := b.Preview()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CSV importer", "Keep quoted newlines", "data:image/webp;base64,", "data:application/pdf;base64,", "review &amp; notes.pdf"} {
		if !bytes.Contains(view, []byte(want)) {
			t.Fatalf("missing preview content %q", want)
		}
	}
	again, _, err := Create(raw, []string{image, doc})
	if err != nil || !bytes.Equal(again, blob) {
		t.Fatal("unstable bundle for identical inputs")
	}
}

func TestManifestMustMatchContents(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("selected note")
	a, err := describe(0, "note.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
	}{
		{"version", func(m *Manifest) { m.Version = 2 }}, {"packet", func(m *Manifest) { m.PacketDigest = packet.Digest(nil) }},
		{"asset", func(m *Manifest) { m.Assets[0].Digest = packet.Digest(nil) }}, {"size", func(m *Manifest) { m.Assets[0].Bytes++ }},
		{"path", func(m *Manifest) { m.Assets[0].Path = "notes.txt" }}, {"type", func(m *Manifest) { m.Assets[0].MediaType = "image/png" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Manifest{1, packet.Digest(raw), []Asset{a}}
			tc.change(&m)
			meta, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			z := zip.NewWriter(&buf)
			for name, content := range map[string][]byte{packetPath: raw, manifestPath: meta, a.Path: data} {
				w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write(content); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(buf.Bytes()); err == nil {
				t.Fatal("accepted inconsistent manifest")
			}
		})
	}
}

func TestSelectionLimits(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/packet/example.beam.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	large := filepath.Join(dir, "oversize")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxAssetBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, paths := range [][]string{nil, make([]string, MaxAssets+1), {dir}, {large}} {
		if _, _, err := Create(raw, paths); err == nil {
			t.Fatal("accepted invalid selection")
		}
	}
	for _, name := range []string{"", "..", "a/b", "a\\b", "a\nb", strings.Repeat("a", 256)} {
		if _, err := describe(0, name, []byte("text")); err == nil {
			t.Fatal("accepted invalid display filename")
		}
	}
	if _, err := describe(0, "empty", nil); err == nil {
		t.Fatal("accepted empty asset")
	}
	if _, err := describe(0, "data.bin", []byte{0, 1, 2, 3}); err == nil {
		t.Fatal("accepted unsupported format")
	}
}
func TestDirectoryLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    uint16
		dirBytes int
		wantOK   bool
	}{
		{"ordinary", 3, 256, true}, {"at limit", MaxAssets + 2, 16 << 10, true}, {"too many members", MaxAssets + 3, 256, false}, {"oversize directory", 3, (16 << 10) + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := make([]byte, tc.dirBytes+22)
			end := raw[tc.dirBytes:]
			copy(end, []byte{'P', 'K', 5, 6})
			binary.LittleEndian.PutUint16(end[8:], tc.count)
			binary.LittleEndian.PutUint16(end[10:], tc.count)
			binary.LittleEndian.PutUint32(end[12:], uint32(tc.dirBytes))
			if (checkDirectory(raw) == nil) != tc.wantOK {
				t.Fatal("directory budget mismatch")
			}
		})
	}
}
func TestWaveUsesPlayableMIME(t *testing.T) {
	data := []byte("RIFF\x04\x00\x00\x00WAVE")
	a, err := describe(0, "sound.wav", data)
	if err != nil || a.MediaType != "audio/wav" || a.Path != "assets/01.wav" {
		t.Fatalf("%+v: %v", a, err)
	}
}
