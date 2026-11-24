// Package localhistory supports bounded, read-only projection of selected local sessions.
package localhistory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type File struct {
	ID, Title, Path string
	Updated         time.Time
}
type Item struct{ Type, Text string }
type History struct {
	Items            []Item
	Title, UpdatedAt string
	Partial          bool
}
type Node struct {
	ID, Parent, Time string
	Items            []Item
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,160}$`)

func Selection(value string) (id, branch string, err error) {
	id, branch, _ = strings.Cut(value, "#")
	if !identifier.MatchString(id) || (strings.Contains(value, "#") && !identifier.MatchString(branch)) {
		return "", "", errors.New("invalid native session or branch selection")
	}
	return id, branch, nil
}

// Branch follows only selected ancestry. A missing ancestor is surfaced as partial history.
func Branch(nodes []Node, leaf string) (History, error) {
	var h History
	byID := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.ID == "" {
			continue
		}
		if _, exists := byID[n.ID]; exists {
			return h, errors.New("duplicate native entry ID")
		}
		byID[n.ID] = i
	}
	if len(nodes) == 0 {
		return h, errors.New("session has no readable entries")
	}
	if leaf == "" {
		leaf = nodes[len(nodes)-1].ID
	}
	if _, ok := byID[leaf]; !ok {
		return h, errors.New("selected branch was not found")
	}
	var path []int
	for id := leaf; id != ""; {
		i, ok := byID[id]
		if !ok {
			h.Partial = true
			break
		}
		path = append(path, i)
		if len(path) > len(nodes) {
			return h, errors.New("cycle in native branch")
		}
		id = nodes[i].Parent
	}
	for i := len(path) - 1; i >= 0; i-- {
		n := nodes[path[i]]
		h.Items = append(h.Items, n.Items...)
		if _, err := time.Parse(time.RFC3339Nano, n.Time); err == nil {
			h.UpdatedAt = n.Time
		}
	}
	return h, nil
}

// Open the exact regular transcript inside the selected store. Other session files are never followed.
func Open(dir, name string) (*os.File, error) {
	if filepath.Base(name) != name {
		return nil, errors.New("invalid session file name")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("session is not a regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		f.Close()
		return nil, errors.New("session file changed before read")
	}
	return f, nil
}

// A very large source fails explicitly. Silently skipping an oversized branch node could join unrelated history.
const MaxSourceBytes = 128 << 20

func Lines(ctx context.Context, file *os.File, visit func([]byte) error) error {
	st, err := file.Stat()
	if err != nil {
		return err
	}
	if st.Size() > MaxSourceBytes {
		return errors.New("native transcript exceeds 128 MiB")
	}
	input := &io.LimitedReader{R: file, N: MaxSourceBytes + 1}
	rows := bufio.NewScanner(input)
	rows.Buffer(make([]byte, 64<<10), 16<<20)
	for rows.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(rows.Bytes()) == 0 {
			continue
		}
		if err := visit(rows.Bytes()); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read native transcript: %w", err)
	}
	if input.N == 0 {
		return errors.New("native transcript grew beyond its read budget")
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if st.Size() != after.Size() || !st.ModTime().Equal(after.ModTime()) {
		return errors.New("source_changed")
	}
	return nil
}
func Names(dir string, suffixes ...string) ([]string, error) {
	d, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	names, err := d.ReadDir(10001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) > 10000 {
		return nil, errors.New("native session inventory exceeds 10000 entries")
	}
	var out []string
	for _, n := range names {
		if !n.Type().IsRegular() {
			continue
		}
		for _, s := range suffixes {
			if strings.HasSuffix(n.Name(), s) {
				out = append(out, n.Name())
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
func Home(env, fallback string) (string, error) {
	if dir := os.Getenv(env); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback), nil
}
func Visible(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
func Message(role, text string) []Item {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	switch role {
	case "user":
		return []Item{{"userMessage", text}}
	case "assistant", "model":
		return []Item{{"agentMessage", text}}
	}
	return nil
}

// Cursor binds a position to an adapter, exact selection, and projected read generation.
type window struct {
	start, end, length int
	revision           string
}

func (w window) next() string {
	if w.end >= w.length {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(w.revision + ":" + strconv.Itoa(w.end)))
}

func page(key string, values any, length int, cursor string, limit int) (window, error) {
	h := sha256.New()
	h.Write([]byte(key))
	h.Write([]byte{0})
	if err := json.NewEncoder(h).Encode(values); err != nil {
		return window{}, err
	}
	rev := hex.EncodeToString(h.Sum(nil)[:16])
	start := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		r, offset, ok := strings.Cut(string(raw), ":")
		if err != nil || !ok || r != rev {
			return window{}, errors.New("source_changed")
		}
		start, err = strconv.Atoi(offset)
		if err != nil || start < 0 || start > length {
			return window{}, errors.New("invalid cursor")
		}
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return window{}, errors.New("limit must be 1..200")
	}
	return window{start: start, end: min(start+limit, length), length: length, revision: rev}, nil
}
