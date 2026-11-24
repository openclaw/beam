package main

import (
	"strings"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/adapters/acp"
	"github.com/openclaw/beam/internal/adapters/claude"
	"github.com/openclaw/beam/internal/adapters/codex"
	"github.com/openclaw/beam/internal/adapters/gemini"
	"github.com/openclaw/beam/internal/adapters/opencode"
	"github.com/openclaw/beam/internal/adapters/pi"
	"github.com/openclaw/beam/internal/adapters/staged"
	"github.com/openclaw/beam/internal/cli"
	"github.com/openclaw/beam/internal/legacy"
	"github.com/openclaw/beam/internal/receiver"
)

func application() cli.App {
	return cli.App{
		Version: version,
		Adapters: cli.Registry{
			"acpx": acp.Adapter{}, "claude": claude.Adapter{}, "codex": codex.Adapter{},
			"opencode": opencode.Adapter{}, "pi": pi.Adapter{}, "gemini": gemini.Adapter{},
		},
		Remote: receiver.Run,
		Hook:   staged.WriteHookNotice,
		Legacy: legacy.Run,
		Dynamic: func(id string) adapter.Adapter {
			prefix, name, ok := strings.Cut(id, ":")
			if ok && (prefix == "acpx" || prefix == "acp") && name != "" {
				return acp.Adapter{Agent: name}
			}
			return nil
		},
	}
}
