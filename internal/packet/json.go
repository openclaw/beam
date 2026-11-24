package packet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"
)

// DecodeClosed enforces the exact JSON field names and required members of a
// struct's JSON tags. encoding/json alone accepts duplicate keys, case aliases,
// missing and null fields, and silently replaces invalid Unicode.
func DecodeClosed(data []byte, maxBytes int, dest any) error {
	if len(data) > maxBytes || !utf8.Valid(data) || !validEscapes(data) {
		return errors.New("JSON exceeds its size limit or contains invalid Unicode")
	}
	t := reflect.TypeOf(dest)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(dest).IsNil() {
		return errors.New("JSON target must be a pointer")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value := reflect.New(t.Elem()).Elem()
	if err := checkValue(d, value, false); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("unexpected trailing JSON data")
	}
	reflect.ValueOf(dest).Elem().Set(value)
	return nil
}

type jsonField struct {
	index    []int
	slot     int
	required bool
	nonempty bool
}

type structFields struct {
	byName map[string]jsonField
	slots  int
}

// Destination types supply cache keys. Keep seen flags local to each object so
// duplicate detection cannot affect a sibling object or a concurrent decode.
var fieldCache sync.Map // reflect.Type -> *structFields

func fieldsFor(t reflect.Type) *structFields {
	if cached, ok := fieldCache.Load(t); ok {
		return cached.(*structFields)
	}
	visible := reflect.VisibleFields(t)
	layout := &structFields{byName: make(map[string]jsonField, len(visible)), slots: len(visible)}
	for i, f := range visible {
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			layout.byName[name] = jsonField{
				index:    f.Index,
				slot:     i,
				required: !strings.HasSuffix(tag, ",omitempty"),
				nonempty: f.Tag.Get("wire") == "nonempty",
			}
		}
	}
	cached, _ := fieldCache.LoadOrStore(t, layout)
	return cached.(*structFields)
}

func checkValue(d *json.Decoder, v reflect.Value, nonempty bool) error {
	if v.Kind() == reflect.Pointer {
		v.Set(reflect.New(v.Type().Elem()))
		v = v.Elem()
	}
	t := v.Type()
	token, err := d.Token()
	if err != nil || token == nil {
		return errors.New("missing or invalid JSON value")
	}
	switch t.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return errors.New("expected JSON object")
		}
		fields := fieldsFor(t)
		seen := make([]bool, fields.slots)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return errors.New("invalid JSON member")
			}
			name, _ := key.(string)
			f, ok := fields.byName[name]
			if !ok || seen[f.slot] {
				return errors.New("unknown or duplicate JSON member")
			}
			seen[f.slot] = true
			if err := checkValue(d, v.FieldByIndex(f.index), f.nonempty); err != nil {
				return err
			}
		}
		for _, f := range fields.byName {
			if f.required && !seen[f.slot] {
				return errors.New("missing required JSON member")
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case reflect.Slice:
		if token != json.Delim('[') {
			return errors.New("expected JSON array")
		}
		v.Set(reflect.MakeSlice(t, 0, 0))
		for d.More() {
			item := reflect.New(t.Elem()).Elem()
			if err := checkValue(d, item, false); err != nil {
				return err
			}
			v.Set(reflect.Append(v, item))
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	case reflect.String:
		s, ok := token.(string)
		if !ok || (nonempty && s == "") {
			return errors.New("expected JSON string")
		}
		v.SetString(s)
	case reflect.Bool:
		b, ok := token.(bool)
		if !ok {
			return errors.New("expected JSON boolean")
		}
		v.SetBool(b)
	case reflect.Int:
		n, ok := token.(json.Number)
		if !ok {
			return errors.New("expected JSON integer")
		}
		x, err := n.Float64()
		if err != nil || x != float64(int64(x)) || v.OverflowInt(int64(x)) {
			return errors.New("expected JSON integer")
		}
		v.SetInt(int64(x))
	default:
		return errors.New("unsupported JSON shape")
	}
	return nil
}

func hex4(b []byte) int {
	n := 0
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			n = 16*n + int(c-'0')
		case c|32 >= 'a' && c|32 <= 'f':
			n = 16*n + int(c|32-'a') + 10
		default:
			return -1
		}
	}
	return n
}

func validEscapes(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) || b[i] != 'u' {
			continue
		}
		if i+5 > len(b) {
			return false
		}
		n := hex4(b[i+1 : i+5])
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+7 > len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low := hex4(b[i+3 : i+7])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
