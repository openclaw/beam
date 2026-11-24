// Package buildversion identifies both release archives and Go module installs.
package buildversion

import (
	"runtime/debug"
	"strings"
)

func Resolve(linked string) string {
	if linked != "" {
		return linked
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}
