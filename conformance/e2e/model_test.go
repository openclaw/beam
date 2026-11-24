//go:build beam_native || beam_openclaw

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type modelCalls struct {
	sync.Mutex
	requests [][]byte
}

func (s *modelCalls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil || !json.Valid(body) {
		http.Error(w, "invalid fixture input", 400)
		return
	}
	s.Lock()
	s.requests = append(s.requests, body)
	s.Unlock()
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.Error(w, "unexpected model route", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	message := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "The fixture received your authorized prompt.", "annotations": []string{}}}}
	for _, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress"}},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{message}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
	} {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
}

func (s *modelCalls) seen() [][]byte {
	s.Lock()
	defer s.Unlock()
	return append([][]byte(nil), s.requests...)
}
