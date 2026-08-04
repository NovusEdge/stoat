package wire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmitterResultLine(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.ResultOK("up", FromVM(sampleVM())); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimRight(buf.String(), "\n")
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("expected exactly one line, got %q", buf.String())
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got["v"] != float64(1) {
		t.Errorf("v = %v, want 1", got["v"])
	}
	if got["type"] != "result" {
		t.Errorf("type = %v, want result", got["type"])
	}
	if got["cmd"] != "up" {
		t.Errorf("cmd = %v, want up", got["cmd"])
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if _, has := got["error"]; has {
		t.Errorf("success result must not carry an error field: %v", got)
	}
}

func TestEmitterErrorResultOmitsData(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.ResultErr("up", MapError(ErrConfirmationRequired)); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	if _, has := got["data"]; has {
		t.Errorf("error result must not carry a data field: %v", got)
	}
	errObj, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error object, got %v", got["error"])
	}
	if errObj["code"] != CodeConfirmationRequired {
		t.Errorf("code = %v, want %s", errObj["code"], CodeConfirmationRequired)
	}
}

func TestEmitterEventHasNoOKField(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.Event(TypeProgress, "pull", map[string]int{"percent": 50}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, has := got["ok"]; has {
		t.Errorf("a non-terminal event must not carry ok: %v", got)
	}
	if got["type"] != "progress" {
		t.Errorf("type = %v, want progress", got["type"])
	}
}

// TestEmitterEscapeHTMLDisabled pins that guest output containing "<"/">"/"&"
// survives byte-for-byte, per §4's SetEscapeHTML(false) requirement.
func TestEmitterEscapeHTMLDisabled(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.ResultOK("exec", FromExecResult(execResultWith("<html>&amp;</html>"))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<html>") {
		t.Errorf("expected the literal, unescaped substring <html> in output: %s", buf.String())
	}
}

// TestEmitterNewlineSafe pins the one hard requirement JSON Lines imposes:
// no literal newline may appear inside a line, even for exec's multi-line
// guest output.
func TestEmitterNewlineSafe(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	multiline := "line one\nline two\nline three"
	if err := e.ResultOK("exec", FromExecResult(execResultWith(multiline))); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("expected exactly one newline (the line terminator), got %d in %q", strings.Count(buf.String(), "\n"), buf.String())
	}
}

func TestSplitJSONFlag(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantJSON bool
		wantRest []string
	}{
		{"global before subcommand", []string{"--json", "ls"}, true, []string{"ls"}},
		{"after subcommand", []string{"ls", "--json"}, true, []string{"ls"}},
		{"none", []string{"ls"}, false, []string{"ls"}},
		// The hard case (§1): exec sends --json to the guest once it is past
		// the VM name; only occurrences before that point are stoat's.
		{"exec: --json before subcommand", []string{"--json", "exec", "work", "ls", "-la"}, true, []string{"exec", "work", "ls", "-la"}},
		{"exec: --json right after exec", []string{"exec", "--json", "work", "ls", "-la"}, true, []string{"exec", "work", "ls", "-la"}},
		{"exec: --json after vm name goes to guest", []string{"exec", "work", "ls", "--json"}, false, []string{"exec", "work", "ls", "--json"}},
		{"exec: --json deep in guest command untouched", []string{"exec", "work", "ls", "-la", "--json", "/tmp"}, false, []string{"exec", "work", "ls", "-la", "--json", "/tmp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotJSON, gotRest := SplitJSONFlag(tt.argv)
			if gotJSON != tt.wantJSON {
				t.Errorf("jsonMode = %v, want %v", gotJSON, tt.wantJSON)
			}
			if !slicesEqual(gotRest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", gotRest, tt.wantRest)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
