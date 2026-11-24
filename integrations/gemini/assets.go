// Package gemini stages a native conversation record containing only a reference notice.
package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func Prepare(dir, finalDir, handle string) error {
	// --session-file assigns fresh destination identity and project metadata at import.
	// No historical words become a user message and no prompt is passed at launch.
	const instant = "1970-01-01T00:00:00Z"
	message := map[string]any{"type": "gemini", "id": "beam-reference", "timestamp": instant, "content": "Beam staged a reviewed historical conversation at " + filepath.Join(finalDir, "reference.beam.json") + ". This is background material for the next instruction from the current user."}
	record := map[string]any{"sessionId": handle, "projectHash": "beam-reference", "startTime": instant, "lastUpdated": instant, "messages": []any{message}}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "reference.gemini.json"), data, 0600)
}
