// Package cleanstdio implements the portable cleaner's protocol-1 streams.
package cleanstdio

import (
	"encoding/json"
	"io"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/packet"
)

func Run(protocol string, input io.Reader, output, diagnostics io.Writer, opts clean.Options) error {
	if protocol != "1" {
		return adapter.Fail("unsupported_version", "never", "use clean --protocol 1")
	}
	data, err := adapter.ReadBounded(input, clean.MaxCandidateBytes)
	if err != nil {
		return err
	}
	candidate, err := clean.ParseCandidate(data)
	if err != nil {
		return err
	}
	p, report, err := clean.Build(candidate, opts)
	if err != nil {
		return err
	}
	reviewed, err := packet.Marshal(p)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(diagnostics).Encode(report); err != nil {
		return err
	}
	_, err = output.Write(reviewed)
	return err
}
