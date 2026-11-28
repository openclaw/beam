package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"slices"

	"github.com/openclaw/beam/internal/bundle"
	"github.com/openclaw/beam/internal/packet"
)

const screenshotContentType = "application/vnd.openclaw.beam.screenshot+json"

func (c *Client) shareScreenshot(ctx context.Context, archive []byte, selectedID string) (Publication, error) {
	b, err := bundle.Read(archive)
	if err != nil {
		return Publication{}, err
	}
	p, err := packet.Parse(b.PacketBytes)
	if err != nil {
		return Publication{}, failure("review_v2_packet_first", "never")
	}
	if selectedID != "" && selectedID != p.BeamID {
		return Publication{}, failure("beam_id_mismatch", "never")
	}
	name, data, ok := b.SinglePNG()
	if !ok {
		return Publication{}, failure("one_png_bundle_required", "never")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || uint64(cfg.Width)*uint64(cfg.Height) > 16_777_216 || !bytes.HasSuffix(data, []byte("\x00\x00\x00\x00IEND\xae\x42\x60\x82")) {
		return Publication{}, failure("invalid_screenshot", "never")
	}
	if c.legacy {
		return Publication{}, failure("screenshot_unsupported", "never")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return Publication{}, err
	}
	if err := require(caps, "publish"); err != nil {
		return Publication{}, err
	}
	if !slices.Contains(caps.PacketVersions, p.Version) {
		return Publication{}, failure("unsupported_packet_version", "never")
	}
	if !slices.Contains(caps.Operations, "publishScreenshot") || caps.MaxScreenshotBytes <= 0 {
		return Publication{}, failure("screenshot_unsupported", "never")
	}
	if len(data) > caps.MaxScreenshotBytes || len(b.PacketBytes) > caps.MaxBodyBytes {
		return Publication{}, failure("too_large", "never")
	}
	body, err := json.Marshal(struct {
		Packet string `json:"packet"`
		Name   string `json:"name"`
		PNG    []byte `json:"png"`
	}{string(b.PacketBytes), name, data})
	if err != nil {
		return Publication{}, err
	}
	// Bind the display filename as well as the image bytes, independently of the packet digest.
	identity := packet.Digest(append([]byte(name+"\x00"), data...))
	return c.publish(ctx, p.BeamID, body, b.Manifest.PacketDigest, identity, false)
}
