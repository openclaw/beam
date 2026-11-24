// Package nativepage pages an immutable projection of a native session.
package nativepage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/openclaw/beam/internal/adapter"
)

// Slice binds the cursor to both selection and content. Native session APIs
// without snapshot tokens can otherwise splice turns from different revisions.
func Slice[T any](selection string, values []T, cursor string, limit int) ([]T, string, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, "", adapter.Fail("invalid_input", "never", "page limit must be between 1 and 200")
	}
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	if err := encoder.Encode(selection); err != nil {
		return nil, "", err
	}
	if err := encoder.Encode(values); err != nil {
		return nil, "", err
	}
	generation := hex.EncodeToString(digest.Sum(nil)[:16])
	offset := 0
	if cursor != "" {
		token, index, ok := strings.Cut(cursor, ".")
		n, err := strconv.Atoi(index)
		if !ok || err != nil || n < 0 || n > len(values) || token != generation {
			return nil, "", adapter.Fail("source_changed", "never", "restart the selected read from its first page")
		}
		offset = n
	}
	end := min(len(values), offset+limit)
	next := ""
	if end < len(values) {
		next = generation + "." + strconv.Itoa(end)
	}
	return values[offset:end], next, nil
}
