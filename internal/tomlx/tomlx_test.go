package tomlx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type doc struct {
	Schema int    `toml:"schema"`
	Name   string `toml:"name"`
	Nested struct {
		A string `toml:"a"`
	} `toml:"nested"`
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecodeRejectsUnknownKey(t *testing.T) {
	p := write(t, "name = \"x\"\n[nested]\nb = 1\n")
	var d doc
	err := Decode(p, &d, Reject)
	if err == nil {
		t.Fatal("unknown key accepted")
	}
	for _, want := range []string{p, `unknown key "nested.b"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestDecodeWarnsUnknownKey(t *testing.T) {
	p := write(t, "name = \"x\"\ncpus_ = 2\n")
	var d doc
	var w bytes.Buffer
	if err := Decode(p, &d, Warn(&w)); err != nil {
		t.Fatal(err)
	}
	if d.Name != "x" {
		t.Errorf("name = %q", d.Name)
	}
	if !strings.Contains(w.String(), `unknown key "cpus_"`) {
		t.Errorf("no warning written: %q", w.String())
	}
}

func TestDecodeWrapsPath(t *testing.T) {
	p := write(t, "name = \n")
	var d doc
	err := Decode(p, &d)
	if err == nil || !strings.Contains(err.Error(), p) {
		t.Errorf("error %v lacks the path", err)
	}
}

func TestDecodeSchemaTooNew(t *testing.T) {
	p := write(t, "schema = 2\nname = \"x\"\n")
	var d doc
	err := Decode(p, &d, Schema(1))
	if err == nil || !strings.Contains(err.Error(), "schema 2 is newer than this stoat (1)") {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeSchemaAbsentIsFine(t *testing.T) {
	p := write(t, "name = \"x\"\n")
	var d doc
	if err := Decode(p, &d, Schema(1)); err != nil {
		t.Fatal(err)
	}
	if d.Schema != 0 {
		t.Errorf("schema = %d, want 0 (absent)", d.Schema)
	}
}
