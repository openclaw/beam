package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Check member names before unmarshalling. The standard decoder otherwise accepts
// duplicate keys and folds case for struct fields, including the protocol envelope.
func members(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return nil, errors.New("expected a JSON object")
	}
	for key := range out {
		valid := false
		for _, k := range allowed {
			if k == key {
				valid = true
				break
			}
		}
		if !valid {
			return nil, errors.New("unrecognized JSON member")
		}
	}
	return out, nil
}

func checkWire(data []byte) error {
	if len(data) > MaxResponseBytes || !utf8.Valid(data) {
		return errors.New("invalid UTF-8 or oversized machine document")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := consumeValue(d, 0); err != nil {
		return err
	}
	_, err := d.Token()
	if err != io.EOF {
		return errors.New("expected one machine document")
	}
	return nil
}
func consumeValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("machine document exceeds nesting limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return errors.New("invalid or duplicate JSON member")
			}
			seen[key] = true
			if err := consumeValue(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('['):
		for d.More() {
			if err := consumeValue(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return nil
}
