// Package pi embeds the per-invocation Pi extension in the native Beam binary.
package pi

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed reference.mjs
var helper []byte

func Prepare(dir, finalDir, handle string) error {
	return os.WriteFile(filepath.Join(dir, "reference.mjs"), helper, 0600)
}
