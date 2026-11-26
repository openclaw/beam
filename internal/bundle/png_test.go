package bundle

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, len(payload)+12)
	binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func TestStripPNGMetadata(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{
		color.NRGBA{R: 241, A: 127}, color.NRGBA{B: 77, A: 255},
	})
	img.SetColorIndex(1, 0, 1)
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	// Insert real chunks after IHDR, and unrelated bytes after IEND.
	raw := bytes.Clone(source.Bytes()[:33])
	raw = append(raw, pngChunk("tEXt", []byte("Author\x00private author"))...)
	raw = append(raw, pngChunk("eXIf", []byte("II\x2a\x00\x08\x00\x00\x00\x00\x00\x00\x00\x00\x00"))...)
	raw = append(raw, source.Bytes()[33:]...)
	raw = append(raw, "private trailer"...)
	clean, err := stripPNGMetadata(raw, MaxAssetBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"private author", "eXIf", "private trailer"} {
		if bytes.Contains(clean, []byte(unwanted)) {
			t.Fatalf("retained %q", unwanted)
		}
	}
	out, err := png.Decode(bytes.NewReader(clean))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != out.Bounds() {
		t.Fatal("image bounds changed")
	}
	for x := range img.Bounds().Dx() {
		if color.NRGBAModel.Convert(out.At(x, 0)) != color.NRGBAModel.Convert(img.At(x, 0)) {
			t.Fatal("indexed color or transparency changed")
		}
	}
	if _, err := stripPNGMetadata(raw, len(clean)-1); err == nil {
		t.Fatal("exceeded output budget")
	}
}

func TestStripPNGRefusesUnsupportedInputs(t *testing.T) {
	var src bytes.Buffer
	if err := png.Encode(&src, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	raw := src.Bytes()
	withChunk := func(chunk []byte) []byte {
		return append(append(bytes.Clone(raw[:33]), chunk...), raw[33:]...)
	}
	shape := func(width, height uint32) []byte {
		header := bytes.Clone(raw[16:29])
		binary.BigEndian.PutUint32(header, width)
		binary.BigEndian.PutUint32(header[4:], height)
		return append(append(bytes.Clone(raw[:8]), pngChunk("IHDR", header)...), raw[33:]...)
	}
	for _, tc := range []struct {
		name, want string
		data       []byte
	}{
		{"animated", "animated PNG", withChunk(pngChunk("acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0}))},
		{"unknown critical", "critical PNG chunk", withChunk(pngChunk("ABCD", nil))},
		{"wide", "pixels", shape(32769, 1)},
		{"area", "pixels", shape(4097, 4096)},
		{"short IDAT", "truncated", raw[:45]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stripPNGMetadata(tc.data, MaxAssetBytes); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
