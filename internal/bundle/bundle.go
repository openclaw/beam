// Package bundle carries the exact reviewed packet and explicitly selected files in a portable ZIP.
package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openclaw/beam/internal/adapters/file"
	"github.com/openclaw/beam/internal/packet"
)

const (
	MaxAssets        = 16
	MaxAssetBytes    = 16 << 20
	MaxTotalBytes    = 32 << 20
	MaxArchiveBytes  = MaxTotalBytes + (128 << 10)
	packetPath       = "packet.beam.json"
	manifestPath     = "manifest.json"
	maxManifestBytes = 16 << 10
)

type Asset struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Bytes     int    `json:"bytes"`
	Digest    string `json:"digest"`
}

type Manifest struct {
	Version      int     `json:"version"`
	PacketDigest string  `json:"packetDigest"`
	Assets       []Asset `json:"assets"`
}

type Bundle struct {
	Manifest Manifest
	Packet   packet.Packet
	files    [][]byte
}

var extensions = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
	"application/pdf": "pdf", "text/plain": "txt",
	"audio/wav": "wav", "audio/mpeg": "mp3", "video/mp4": "mp4", "video/webm": "webm",
}

func describe(index int, name string, data []byte) (Asset, error) {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return Asset{}, errors.New("asset needs a display filename of 1 to 255 bytes without separators or control characters")
	}
	mediaType, _, _ := strings.Cut(http.DetectContentType(data), ";")
	// Go's sniffer returns audio/wave; browsers recognize audio/wav for playback.
	if mediaType == "audio/wave" {
		mediaType = "audio/wav"
	}
	ext, ok := extensions[mediaType]
	if !ok || len(data) == 0 || (mediaType == "text/plain" && !utf8.Valid(data)) {
		return Asset{}, fmt.Errorf("unsupported or empty asset %q: choose an image, PDF, UTF-8 text, audio or video file", name)
	}
	return Asset{Name: name, Path: fmt.Sprintf("assets/%02d.%s", index+1, ext), MediaType: mediaType, Bytes: len(data), Digest: packet.Digest(data)}, nil
}

func readSelection(name string, limit int) ([]byte, error) {
	// Check the file type before opening (a FIFO may block), then check the opened file.
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("choose a regular file within the attachment byte limits")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("selected attachment changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("attachment exceeds bundle byte limits")
	}
	return data, nil
}

func Create(raw []byte, paths []string) ([]byte, Manifest, error) {
	var buf bytes.Buffer
	m := Manifest{Version: 1, PacketDigest: packet.Digest(raw)}
	if _, err := file.ReadPacket(raw); err != nil {
		return nil, m, err
	}
	if len(paths) < 1 || len(paths) > MaxAssets {
		return nil, m, fmt.Errorf("select 1 to %d files with --attach", MaxAssets)
	}
	z := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := add(packetPath, raw); err != nil {
		return nil, m, err
	}
	remaining := MaxTotalBytes
	for i, path := range paths {
		data, err := readSelection(path, min(remaining, MaxAssetBytes))
		if err != nil {
			return nil, m, err
		}
		asset, err := describe(i, filepath.Base(path), data)
		if err != nil {
			return nil, m, err
		}
		if err := add(asset.Path, data); err != nil {
			return nil, m, err
		}
		m.Assets = append(m.Assets, asset)
		remaining -= len(data)
	}
	meta, err := json.Marshal(m)
	if err != nil {
		return nil, m, err
	}
	if len(meta) > maxManifestBytes {
		return nil, m, errors.New("asset manifest exceeds its byte limit")
	}
	if err := add(manifestPath, meta); err != nil {
		return nil, m, err
	}
	if err := z.Close(); err != nil {
		return nil, m, err
	}
	return buf.Bytes(), m, nil
}

// V1 uses single-disk ZIP32 with no archive comment. Bound directory metadata
// before archive/zip allocates File objects; the payload budget alone is much larger.
func checkDirectory(blob []byte) error {
	const endBytes = 22
	if len(blob) < endBytes || len(blob) > MaxArchiveBytes {
		return errors.New("invalid bundle archive size")
	}
	end := blob[len(blob)-endBytes:]
	le := binary.LittleEndian
	if string(end[:4]) != "PK\x05\x06" || le.Uint32(end[4:8]) != 0 || le.Uint16(end[20:]) != 0 {
		return errors.New("expected a single-disk ZIP32 bundle without an archive comment")
	}
	count := le.Uint16(end[10:12])
	size := le.Uint32(end[12:16])
	offset := le.Uint32(end[16:20])
	if count < 3 || count > MaxAssets+2 || count != le.Uint16(end[8:10]) || size > 16<<10 || uint64(offset)+uint64(size) != uint64(len(blob)-endBytes) {
		return errors.New("unexpected bundle ZIP directory size or member count")
	}
	return nil
}

func Read(blob []byte) (*Bundle, error) {
	if err := checkDirectory(blob); err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, err
	}
	if len(z.File) < 3 || len(z.File) > MaxAssets+2 {
		return nil, errors.New("unexpected number of bundle members")
	}
	members := make(map[string]*zip.File, len(z.File))
	for _, f := range z.File {
		if f.Method != zip.Store || !f.Mode().IsRegular() || members[f.Name] != nil {
			return nil, errors.New("expected unique, stored regular ZIP entries")
		}
		members[f.Name] = f
	}
	take := func(name string, limit int) ([]byte, error) {
		f := members[name]
		if f == nil || f.UncompressedSize64 > uint64(limit) || f.CompressedSize64 != f.UncompressedSize64 {
			return nil, fmt.Errorf("missing or oversized bundle member %q", name)
		}
		delete(members, name)
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
		if err != nil {
			return nil, err
		}
		if len(data) > limit || uint64(len(data)) != f.UncompressedSize64 {
			return nil, errors.New("bundle member size mismatch")
		}
		return data, nil
	}
	meta, err := take(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, err
	}
	b := new(Bundle)
	if err := packet.DecodeClosed(meta, maxManifestBytes, &b.Manifest); err != nil {
		return nil, err
	}
	if b.Manifest.Version != 1 || len(b.Manifest.Assets) != len(z.File)-2 {
		return nil, errors.New("unsupported or incomplete bundle manifest")
	}
	raw, err := take(packetPath, packet.MaxBytes)
	if err != nil {
		return nil, err
	}
	if packet.Digest(raw) != b.Manifest.PacketDigest {
		return nil, errors.New("bundled packet digest mismatch")
	}
	b.Packet, err = file.ReadPacket(raw)
	if err != nil {
		return nil, err
	}
	remaining := MaxTotalBytes
	for i, asset := range b.Manifest.Assets {
		data, err := take(asset.Path, min(remaining, MaxAssetBytes))
		if err != nil {
			return nil, err
		}
		expected, err := describe(i, asset.Name, data)
		if err != nil {
			return nil, err
		}
		if asset != expected {
			return nil, fmt.Errorf("asset %d does not match its manifest", i+1)
		}
		b.files = append(b.files, data)
		remaining -= len(data)
	}
	return b, nil
}
