// Package tomlx is the one TOML decoder for vm.toml, recipe.toml, and
// guest.toml. It wraps the file path into every error, reports keys the
// target type does not declare, and bounds a file's schema version.
package tomlx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
	gotoml "github.com/pelletier/go-toml/v2"
)

type options struct {
	warn      io.Writer // nil means unknown keys are an error
	schemaMax int       // 0 means no schema check
}

// Option adjusts one Decode call.
type Option func(*options)

// Reject makes an unknown key an error. It is the default and exists so a
// call site can say so.
var Reject Option = func(*options) {}

// Warn writes one line per unknown key to w and keeps going. vm.toml uses
// it: a key an older stoat wrote must not mark the VM broken.
func Warn(w io.Writer) Option { return func(o *options) { o.warn = w } }

// Schema errors when the file's top-level `schema` is greater than max. An
// absent schema passes; the caller decides what absence means.
func Schema(max int) Option { return func(o *options) { o.schemaMax = max } }

// Decode reads path into v.
func Decode(path string, v any, opts ...Option) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	_, err := decode(path, v, o)
	return err
}

// DecodeDefined behaves like Decode but also reports, for each of keys,
// whether that top-level key was present in path's TOML. It parses the file
// once: config.Load uses it to tell an explicit legacy key from a field a
// decode seeded itself, without a second read of the same file.
func DecodeDefined(path string, v any, keys []string, opts ...Option) ([]bool, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	md, err := decode(path, v, o)
	if err != nil {
		return nil, err
	}
	defined := make([]bool, len(keys))
	for i, k := range keys {
		defined[i] = md.IsDefined(k)
	}
	return defined, nil
}

func decode(path string, v any, o options) (toml.MetaData, error) {
	md, err := toml.DecodeFile(path, v)
	if err != nil {
		return md, fmt.Errorf("%s: %w", path, err)
	}
	if o.schemaMax > 0 && md.IsDefined("schema") {
		var s struct {
			Schema int `toml:"schema"`
		}
		if _, err := toml.DecodeFile(path, &s); err != nil {
			return md, fmt.Errorf("%s: %w", path, err)
		}
		if s.Schema > o.schemaMax {
			return md, fmt.Errorf("%s: schema %d is newer than this stoat (%d)", path, s.Schema, o.schemaMax)
		}
	}
	for _, k := range md.Undecoded() {
		if insideDynamicField(reflect.TypeOf(v), k) {
			continue
		}
		key := strings.Join(k, ".")
		if o.warn == nil {
			return md, fmt.Errorf("%s: unknown key %q", path, key)
		}
		fmt.Fprintf(o.warn, "%s: unknown key %q\n", path, key)
	}
	return md, nil
}

// insideDynamicField reports whether key names a value nested under a field
// whose Go type carries an interface{} somewhere on its path, such as
// map[string]any. BurntSushi's TOML decoder places a table value fully into
// such a field but still lists its nested keys as undecoded, because it
// tracks only which struct fields it used, not which map entries it filled.
// Any key under an interface{} is definitionally known: an untyped value
// accepts every shape by construction. A map keyed into a concrete struct
// (map[string]VM) is not dynamic; its element's own fields are still checked,
// only the map key itself (arbitrary, one segment) is skipped.
func insideDynamicField(t reflect.Type, key []string) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface {
		return true
	}
	if len(key) == 0 {
		return false
	}
	switch t.Kind() {
	case reflect.Map:
		if t.Elem().Kind() == reflect.Interface {
			return true
		}
		return insideDynamicField(t.Elem(), key[1:])
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("toml"), ",")[0]
			if name == "" {
				name = f.Name
			}
			if name == key[0] {
				return insideDynamicField(f.Type, key[1:])
			}
		}
	}
	return false
}

// Encode is the single TOML writer for files owned by stoat.
func Encode(path string, v any) error {
	var buf bytes.Buffer
	if err := gotoml.NewEncoder(&buf).Encode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	data := ownedTableComments(normalizeQuotes(buf.Bytes()))
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// normalizeQuotes keeps files written by stoat compatible with its existing
// examples, which use basic double-quoted strings. go-toml prefers literal
// single-quoted strings when the value does not need escaping.
func normalizeQuotes(data []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(data); {
		if data[i] == '"' {
			out.WriteByte(data[i])
			i++
			for i < len(data) {
				out.WriteByte(data[i])
				if data[i] == '\\' && i+1 < len(data) {
					i++
					out.WriteByte(data[i])
				} else if data[i] == '"' {
					i++
					break
				}
				i++
			}
			continue
		}
		if data[i] != '\'' || i+1 >= len(data) || data[i+1] == '\'' {
			out.WriteByte(data[i])
			i++
			continue
		}
		end := i + 1
		for end < len(data) && data[end] != '\'' && data[end] != '\n' {
			end++
		}
		if end == len(data) || data[end] == '\n' {
			out.WriteByte(data[i])
			i++
			continue
		}
		out.WriteByte('"')
		for _, c := range data[i+1 : end] {
			if c == '\\' || c == '"' {
				out.WriteByte('\\')
			}
			out.WriteByte(c)
		}
		out.WriteByte('"')
		i = end + 1
	}
	return out.Bytes()
}

// ownedTableComments repeats a struct field's ownership marker for nested
// tables. go-toml emits a field comment once for a map, while vm.toml has one
// owned table for each recipe and each recipe's outputs.
func ownedTableComments(data []byte) []byte {
	const marker = "# written by stoat; do not edit"
	if !bytes.Contains(data, []byte(marker)) {
		return data
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+4)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[params") || strings.HasPrefix(trimmed, "[applied") {
			j := len(out) - 1
			for j >= 0 && strings.TrimSpace(out[j]) == "" {
				j--
			}
			if j < 0 || !strings.Contains(out[j], marker) {
				out = append(out, marker)
			}
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}
