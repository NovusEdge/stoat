package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
)

// runJSON runs Main with --json and returns every stdout line decoded. It
// fails the test on the first line that is not a JSON object, which is the
// property the whole contract rests on: a consumer calls json.loads on each
// line and one stray byte of prose breaks every command, not just this one.
func runJSON(t *testing.T, argv ...string) (int, []map[string]any) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(append([]string{"--json"}, argv...), "test-version", strings.NewReader(""), &out, &errOut)

	var objs []map[string]any
	for i, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("%v: stdout line %d is not JSON: %v\nline: %q", argv, i, err, line)
		}
		objs = append(objs, obj)
	}
	return code, objs
}

// result returns the single terminal line, failing if there is not exactly
// one or if it is not last. "exactly one result, always last" is what lets a
// consumer read until EOF and take the last result without ambiguity.
func result(t *testing.T, objs []map[string]any) map[string]any {
	t.Helper()
	n := 0
	for i, o := range objs {
		if o["type"] != wire.TypeResult {
			continue
		}
		n++
		if i != len(objs)-1 {
			t.Errorf("result line at %d of %d, want last", i, len(objs)-1)
		}
	}
	if n != 1 {
		t.Fatalf("got %d result lines, want exactly 1: %v", n, objs)
	}
	return objs[len(objs)-1]
}

// TestJSONEnvelopeEveryCommand is the end-to-end gate: every subcommand
// reachable without a booted VM or a network answers in the contract. A
// command added without a --json branch prints its human text and fails here
// on "not JSON", which is the point.
func TestJSONEnvelopeEveryCommand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		ok   bool
		code string // error.code when ok is false
		exit int
	}{
		{name: "ls", argv: []string{"ls"}, ok: true, exit: ExitOK},
		{name: "version", argv: []string{"version"}, ok: true, exit: ExitOK},
		{name: "help", argv: []string{"help"}, ok: true, exit: ExitOK},
		{name: "images", argv: []string{"images"}, ok: true, exit: ExitOK},
		{name: "prune", argv: []string{"prune"}, ok: true, exit: ExitOK},
		{name: "logs", argv: []string{"logs"}, ok: true, exit: ExitOK},
		{name: "recipe list", argv: []string{"recipe", "list"}, ok: true, exit: ExitOK},
		{name: "guest ls", argv: []string{"guest", "ls"}, ok: true, exit: ExitOK},
		{name: "guest show", argv: []string{"guest", "show", "alpine"}, ok: true, exit: ExitOK},
		{name: "guest show unknown", argv: []string{"guest", "show", "plan9"}, code: wire.CodeNotFound, exit: ExitFail},
		{name: "forward show", argv: []string{"forward", "work"}, ok: true, exit: ExitOK},
		// The fixture is a live VM, which has no qcow2 to snapshot; the point
		// here is the envelope, and no_disk is the honest answer for it.
		{name: "snapshot list", argv: []string{"snapshot", "work"}, code: wire.CodeNoDisk, exit: ExitFail},
		// doctor exits 0 even when the host fails a check: it succeeded at
		// checking, and exit 1 means stoat failed to answer.
		{name: "doctor", argv: []string{"doctor"}, ok: true, exit: ExitOK},
		// The plan is computed host-side, so a stopped fixture VM answers it.
		{name: "apply dry-run", argv: []string{"apply", "work", "--dry-run"}, ok: true, exit: ExitOK},

		{name: "up unknown", argv: []string{"up", "nope"}, code: wire.CodeNotFound, exit: ExitFail},
		{name: "down stopped", argv: []string{"down", "work"}, code: wire.CodeNotRunning, exit: ExitFail},
		// --json never prompts: rm without -y is an error, not a question.
		{name: "rm no -y", argv: []string{"rm", "work"}, code: wire.CodeConfirmationRequired, exit: ExitFail},
		{name: "unknown subcommand", argv: []string{"frobnicate"}, code: wire.CodeUsage, exit: ExitUsage},
		{name: "missing arg", argv: []string{"up"}, code: wire.CodeUsage, exit: ExitUsage},
		// ssh cannot answer at all: syscall.Exec leaves no process to write
		// the result line, so it refuses rather than faking one.
		{name: "ssh refused", argv: []string{"ssh", "work"}, code: wire.CodeUsage, exit: ExitUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliRoot(t)
			if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
				t.Fatal(err)
			}

			code, objs := runJSON(t, tt.argv...)
			if code != tt.exit {
				t.Errorf("exit = %d, want %d (%v)", code, tt.exit, objs)
			}
			res := result(t, objs)
			if v, _ := res["v"].(float64); int(v) != wire.ContractVersion {
				t.Errorf("v = %v, want %d", res["v"], wire.ContractVersion)
			}
			if res["cmd"] != tt.argv[0] {
				t.Errorf("cmd = %v, want %q", res["cmd"], tt.argv[0])
			}
			if res["ok"] != tt.ok {
				t.Errorf("ok = %v, want %v", res["ok"], tt.ok)
			}
			if tt.ok {
				// data and error are mutually exclusive, and a consumer
				// branching on "ok" must never disagree with one branching on
				// the presence of "error".
				if _, bad := res["error"]; bad {
					t.Errorf("ok result carries an error field: %v", res)
				}
				// json.md's envelope table types data as an object. apply
				// --dry-run shipped a bare array here, which broke a consumer
				// that indexes data by field name.
				if d, present := res["data"]; present {
					if _, obj := d.(map[string]any); !obj {
						t.Errorf("data is %T, want an object: %v", d, res)
					}
				}
				return
			}
			errObj, _ := res["error"].(map[string]any)
			if errObj == nil {
				t.Fatalf("failed result has no error object: %v", res)
			}
			if errObj["code"] != tt.code {
				t.Errorf("error.code = %v, want %q", errObj["code"], tt.code)
			}
			if msg, _ := errObj["message"].(string); msg == "" {
				t.Errorf("error.message is empty: %v", errObj)
			}
		})
	}
}

// A nil slice marshals as null in Go, and a Python `for vm in data["vms"]`
// raises TypeError on null. An empty data root is the case that produces it.
func TestJSONEmptyListsAreArraysNotNull(t *testing.T) {
	cliRoot(t)
	_, objs := runJSON(t, "ls")
	data, _ := result(t, objs)["data"].(map[string]any)
	if _, ok := data["vms"].([]any); !ok {
		t.Errorf("vms = %#v, want an empty array", data["vms"])
	}
}

// The three fields that must never reach the wire. Asserted here, outside
// package wire, because this is the path a real consumer sees: the DTO could
// be correct and a run body could still hand raw core types to the encoder.
func TestJSONNeverLeaksHostPathsOrConsolePassword(t *testing.T) {
	cliRoot(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, ConsolePassword: "deadbeefcafe"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "ls")
	raw, err := json.Marshal(objs)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"deadbeefcafe", "console_password", "paths", v.Dir} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("ls output leaks %q: %s", leak, raw)
		}
	}
}

// --json implies non-interactive: rm must not read stdin looking for a "y",
// or an MCP server that pipes a stray newline gets a VM deleted.
func TestJSONRMNeverReadsStdin(t *testing.T) {
	cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	stdin := strings.NewReader("y\n")
	if code := Main([]string{"--json", "rm", "work"}, "test", stdin, &out, &errOut); code != ExitFail {
		t.Fatalf("exit = %d, want %d: %s", code, ExitFail, out.String())
	}
	if n := stdin.Len(); n != len("y\n") {
		t.Errorf("rm consumed %d bytes of stdin, want 0", len("y\n")-n)
	}
	if _, err := config.Load("work"); err != nil {
		t.Errorf("work was deleted: %v", err)
	}
}

// Under --json errors go to STDOUT, not stderr (§4): a consumer that has to
// merge two pipes to read one result will eventually interleave them wrong,
// and reading them sequentially deadlocks when either buffer fills.
func TestJSONErrorsGoToStdout(t *testing.T) {
	cliRoot(t)
	var out, errOut bytes.Buffer
	Main([]string{"--json", "up", "nope"}, "test", nil, &out, &errOut)
	if !strings.Contains(out.String(), `"not_found"`) {
		t.Errorf("stdout has no error envelope: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

// exec's guest command is verbatim: a --json after the VM name belongs to the
// guest, so the scan must stop consuming it there.
func TestJSONFlagStopsAtExecsCommand(t *testing.T) {
	jsonMode, rest := wire.SplitJSONFlag([]string{"exec", "work", "ls", "--json"})
	if jsonMode {
		t.Error("--json after exec's vm name was eaten by stoat")
	}
	if got := strings.Join(rest, " "); got != "exec work ls --json" {
		t.Errorf("rest = %q, want the argv unchanged", got)
	}
}
