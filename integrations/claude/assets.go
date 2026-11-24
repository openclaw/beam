// Package claude packages the per-invocation Claude Code hook without global settings changes.
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func Prepare(dir, finalDir, handle string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	// Claude's command hooks use Bash, including Git Bash on Windows, unless shell is overridden.
	command := "'" + strings.ReplaceAll(filepath.ToSlash(bin), "'", "'\"'\"'") + "' hook-notice claude " + handle
	hooks := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"matcher": "startup", "hooks": []any{map[string]string{"type": "command", "command": command}}}}}}
	cfg, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	root := filepath.Join(dir, "bridge")
	manifest := filepath.Join(root, ".claude-plugin")
	if err := os.MkdirAll(manifest, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(manifest, "plugin.json"), []byte(`{"name":"beam-reference","version":"1.0.0","description":"Show a reviewed local Beam reference when a new conversation starts."}`), 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "hooks", "hooks.json"), cfg, 0600)
}
