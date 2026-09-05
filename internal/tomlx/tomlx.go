// Package tomlx is the one TOML decoder for vm.toml, recipe.toml, and
// guest.toml. It wraps the file path into every error, reports keys the
// target type does not declare, and bounds a file's schema version.
package tomlx

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
	md, err := toml.DecodeFile(path, v)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if o.schemaMax > 0 && md.IsDefined("schema") {
		var s struct {
			Schema int `toml:"schema"`
		}
		if _, err := toml.DecodeFile(path, &s); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if s.Schema > o.schemaMax {
			return fmt.Errorf("%s: schema %d is newer than this stoat (%d)", path, s.Schema, o.schemaMax)
		}
	}
	for _, k := range md.Undecoded() {
		key := strings.Join(k, ".")
		if o.warn == nil {
			return fmt.Errorf("%s: unknown key %q", path, key)
		}
		fmt.Fprintf(o.warn, "%s: unknown key %q\n", path, key)
	}
	return nil
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
