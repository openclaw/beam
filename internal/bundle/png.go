package bundle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
)

type pngOutput struct {
	buf   bytes.Buffer
	limit int
}

func (w *pngOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buf.Len() {
		return 0, errors.New("encoded PNG exceeds bundle byte limits")
	}
	return w.buf.Write(p)
}

func stripPNGMetadata(data []byte, limit int) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	// Bound both the decoded image and the encoder's per-scanline buffers for wide screenshots.
	if cfg.Width > 32768 || cfg.Height > 32768 || uint64(cfg.Width)*uint64(cfg.Height) > 16_777_216 {
		return nil, errors.New("PNG exceeds 32,768 pixels per side or 16,777,216 total pixels")
	}
	// image/png skips unfamiliar chunks, including animation. Re-encoding such an image
	// could silently discard a frame or a critical feature. Stop at IEND to drop trailers.
	for chunks := data[8:]; len(chunks) >= 12; {
		length := binary.BigEndian.Uint32(chunks)
		if uint64(length) > uint64(len(chunks)-12) {
			return nil, errors.New("truncated PNG chunk")
		}
		kind := string(chunks[4:8])
		if kind == "IEND" {
			break
		}
		switch kind {
		case "acTL", "fcTL", "fdAT":
			return nil, errors.New("animated PNG: choose a static image to strip metadata")
		case "IHDR", "PLTE", "IDAT":
		default:
			if kind[0]&0x20 == 0 {
				return nil, fmt.Errorf("unsupported critical PNG chunk %q", kind)
			}
		}
		chunks = chunks[12+int(length):]
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	w := pngOutput{limit: limit}
	if err := png.Encode(&w, img); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}
