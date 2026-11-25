package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/openclaw/beam/internal/bundle"
	"github.com/openclaw/beam/internal/packet"
)

func bundleCommand(cmd string, o options, in io.Reader, out io.Writer) (any, error) {
	input := o.args[0]
	dst := o.values["out"]
	if cmd != "bundle inspect" {
		if err := o.require("out"); err != nil {
			return nil, err
		}
		if dst == "-" && o.values["json"] != "" {
			return nil, fmt.Errorf("choose a named --out file with --json")
		}
		if dst != "-" {
			// A converted artifact may replace a previous output, never its own inputs.
			info, err := os.Stat(dst)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if info != nil {
				inputs := o.attachments
				if input == "-" {
					if f, ok := in.(*os.File); ok {
						if src, err := f.Stat(); err == nil && os.SameFile(info, src) {
							return nil, fmt.Errorf("--out must differ from stdin")
						}
					}
				} else {
					inputs = append([]string{input}, inputs...)
				}
				for _, name := range inputs {
					if src, err := os.Stat(name); err == nil && os.SameFile(info, src) {
						return nil, fmt.Errorf("--out must differ from every input")
					}
				}
			}
		}
	}
	limit := int64(bundle.MaxArchiveBytes)
	if cmd == "bundle create" {
		limit = packet.MaxBytes
	}
	raw, err := loadInput(input, in, limit)
	if err != nil {
		return nil, err
	}
	if err := matchDigest(o.values["expect-digest"], raw); err != nil {
		return nil, err
	}
	var m bundle.Manifest
	var artifact []byte
	var digest string
	if cmd == "bundle create" {
		artifact, m, err = bundle.Create(raw, o.attachments)
		if err != nil {
			return nil, err
		}
		digest = packet.Digest(artifact)
	} else {
		b, err := bundle.Read(raw)
		if err != nil {
			return nil, err
		}
		digest = packet.Digest(raw)
		m = b.Manifest
		if cmd == "bundle preview" {
			artifact, err = b.Preview()
			if err != nil {
				return nil, err
			}
		}
	}
	if dst != "" {
		if err := save(dst, artifact, out); err != nil {
			return nil, err
		}
		if dst == "-" {
			return nil, nil
		}
	}
	return struct {
		Digest   string          `json:"bundleDigest"`
		Manifest bundle.Manifest `json:"manifest"`
	}{digest, m}, nil
}
