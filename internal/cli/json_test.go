package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
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
		code wire.Code // error.code when ok is false
		exit int
	}{
		{name: "ls", argv: []string{"ls"}, ok: true, exit: ExitOK},
		{name: "version", argv: []string{"version"}, ok: true, exit: ExitOK},
		{name: "help", argv: []string{"help"}, ok: true, exit: ExitOK},
		{name: "images", argv: []string{"images"}, ok: true, exit: ExitOK},
		{name: "prune", argv: []string{"prune"}, ok: true, exit: ExitOK},
		{name: "logs", argv: []string{"logs"}, ok: true, exit: ExitOK},
		{name: "recipe list", argv: []string{"recipe", "list"}, ok: true, exit: ExitOK},
		{name: "wait healthy and until", argv: []string{"wait", "work", "--healthy", "--until", "applied"}, code: wire.CodeUsage, exit: ExitUsage},
		{name: "recipe show", argv: []string{"recipe", "show", "docker"}, ok: true, exit: ExitOK},
		{name: "recipe show unknown", argv: []string{"recipe", "show", "nope"}, code: wire.CodeNotFound, exit: ExitFail},
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
		// The fixture VM is stopped, so the honest answer is not_running; the
		// point here is that the command answers in the envelope at all.
		{name: "screenshot stopped", argv: []string{"screenshot", "work"}, code: wire.CodeNotRunning, exit: ExitFail},
		// mcp serve blocks on a transport and speaks MCP, not the --json
		// envelope, so it has no row here.
		{name: "mcp doctor", argv: []string{"mcp", "doctor"}, ok: true, exit: ExitOK},

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
			if tt.name == "mcp doctor" {
				t.Setenv("HOME", t.TempDir())
			}
			if tt.name == "recipe show" {
				if err := recipes.Install(); err != nil {
					t.Fatal(err)
				}
			}
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
			got, _ := errObj["code"].(string)
			if got != string(tt.code) {
				t.Errorf("error.code = %v, want %q", errObj["code"], tt.code)
			}
			// A code absent from Codes() is one no consumer can generate a
			// switch for, whatever the message beside it says.
			if !slices.Contains(wire.Codes(), wire.Code(got)) {
				t.Errorf("error.code = %q, which wire.Codes() does not declare", got)
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

// TestLSJSONDataDecodesAsWireVMList pins ls --json's data as wire.VMList's
// exact shape (the house rule: --json data is a wire struct, never an inline
// map[string]any). DisallowUnknownFields catches a stray or renamed top-level
// key that a looser map could carry silently; decoding into the real
// wire.VMList type, not a lookalike, ties this assertion to that struct
// rather than to whatever shape the current call site happens to produce.
func TestLSJSONDataDecodesAsWireVMList(t *testing.T) {
	dir := projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Project: dir}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "ls")
	raw, err := json.Marshal(result(t, objs)["data"])
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var list wire.VMList
	if err := dec.Decode(&list); err != nil {
		t.Fatalf("ls --json data does not decode as wire.VMList: %v\n%s", err, raw)
	}
	if len(list.VMs) != 1 || list.VMs[0].Name != "myrepo-dev" || list.VMs[0].Key != "dev" {
		t.Errorf("ls --json vms = %+v, want one entry for myrepo-dev/dev", list.VMs)
	}
}

// Recipe show is a caller boundary, so pin the named contract payload rather
// than accepting a generic map whose fields can drift from recipe list.
func TestJSONRecipeShowCarriesNamedContract(t *testing.T) {
	cliRoot(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "recipe", "show", "docker")
	if code != ExitOK {
		t.Fatalf("recipe show exit = %d, want %d: %v", code, ExitOK, objs)
	}
	data, ok := result(t, objs)["data"].(map[string]any)
	if !ok {
		t.Fatalf("recipe show data = %#v, want object", result(t, objs)["data"])
	}
	show, ok := data["recipe"].(map[string]any)
	if !ok {
		t.Fatalf("recipe show data.recipe = %#v, want named object", data["recipe"])
	}
	for _, field := range []string{"name", "schema", "params", "outputs", "health"} {
		if _, exists := show[field]; !exists {
			t.Errorf("recipe show omitted %q: %v", field, show)
		}
	}
	if _, ok := show["params"].([]any); !ok {
		t.Errorf("recipe show params = %#v, want array", show["params"])
	}
	if _, ok := show["outputs"].([]any); !ok {
		t.Errorf("recipe show outputs = %#v, want array", show["outputs"])
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
	// v.Dir is the VM's data directory, not project.Project.Dir: wire.VM.Project
	// is a project's host path and is meant to ship, so only v.Dir is checked.
	for _, leak := range []string{"deadbeefcafe", "console_password", "paths", v.Dir} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("ls output leaks %q: %s", leak, raw)
		}
	}
}

// Get is a real wire sink, not just a DTO unit test: a stored secret must be
// represented by the redacted marker in the status detail and never by the
// value loaded from secrets.toml.
func TestJSONGetRedactsStoredSecretValue(t *testing.T) {
	dir := cliRoot(t)
	recipeDir := filepath.Join(dir, "recipes", "redaction")
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(`schema = 3
name = "redaction"
script = "install.sh"

[params.token]
type = "secret"
required = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"redaction"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	const sentinel = "synthetic-secret-sentinel"
	if err := config.SaveSecrets(v.Dir, config.Secrets{"redaction": {"token": sentinel}}); err != nil {
		t.Fatal(err)
	}

	code, objs := runJSON(t, "get", "work")
	if code != ExitOK {
		t.Fatalf("get exit = %d, want %d: %v", code, ExitOK, objs)
	}
	raw, err := json.Marshal(objs)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sentinel)) {
		t.Fatalf("get output leaked secret %q: %s", sentinel, raw)
	}
	data, ok := result(t, objs)["data"].(map[string]any)
	if !ok {
		t.Fatalf("get data = %#v, want object", result(t, objs)["data"])
	}
	vm, ok := data["vm"].(map[string]any)
	if !ok {
		t.Fatalf("get data.vm = %#v, want object", data["vm"])
	}
	detail, ok := vm["recipes_detail"].([]any)
	if !ok || len(detail) != 1 {
		t.Fatalf("recipes_detail = %#v, want one recipe detail", vm["recipes_detail"])
	}
	state, ok := detail[0].(map[string]any)
	if !ok || state["name"] != "redaction" {
		t.Fatalf("recipe detail = %#v, want named redaction state", detail[0])
	}
	params, ok := state["params"].(map[string]any)
	if !ok {
		t.Fatalf("recipe detail params = %#v, want object", state["params"])
	}
	if params["token"] != "<set>" {
		t.Fatalf("recipe detail token = %#v, want <set>", params["token"])
	}
}

// List must not silently omit a VM merely because its secret store is
// unreadable. The error remains tied to the VM so a caller can repair the
// right directory.
func TestJSONListPropagatesInsecureSecretErrorWithVMContext(t *testing.T) {
	dir := cliRoot(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "work", config.SecretsName)
	if err := os.WriteFile(path, []byte("[redaction]\ntoken = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "ls")
	if code != ExitFail {
		t.Fatalf("ls exit = %d, want %d: %v", code, ExitFail, objs)
	}
	raw, _ := json.Marshal(objs)
	if !bytes.Contains(raw, []byte("work")) || !bytes.Contains(raw, []byte(config.SecretsName)) {
		t.Fatalf("insecure secret error lacks VM context: %s", raw)
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

// TestJSONCreateAllowExecMatchesAgentAccess pins allow_exec at the create
// --json boundary: it must track agent_access rather than default to true
// underneath it, so a consumer reading only allow_exec (the field
// agent_access replaces) never sees a non-exec level advertised as exec
// access, or the reverse.
func TestJSONCreateAllowExecMatchesAgentAccess(t *testing.T) {
	dir := cliRoot(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	cases := []struct {
		name   string
		flags  []string
		access string
	}{
		{"vm-none", []string{"--agent-access", "none"}, "none"},
		{"vm-observe", []string{"--agent-access", "observe"}, "observe"},
		{"vm-manage", []string{"--agent-access", "manage"}, "manage"},
		{"vm-exec", []string{"--agent-access", "exec"}, "exec"},
		{"vm-default", nil, "manage"},
		{"vm-allow-exec-alias", []string{"--allow-exec"}, "exec"},
		{"vm-allow-exec-false-alias", []string{"--allow-exec=false"}, "manage"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := append([]string{"create", c.name, "--image", "alpine-virt-3.24.1-x86_64.iso"}, c.flags...)
			code, objs := runJSON(t, argv...)
			if code != ExitOK {
				t.Fatalf("create exit = %d, want %d: %v", code, ExitOK, objs)
			}
			data, ok := result(t, objs)["data"].(map[string]any)
			if !ok {
				t.Fatalf("create data = %#v, want object", result(t, objs)["data"])
			}
			vm, ok := data["vm"].(map[string]any)
			if !ok {
				t.Fatalf("create data.vm = %#v, want object", data["vm"])
			}
			if got := vm["agent_access"]; got != c.access {
				t.Errorf("agent_access = %v, want %q", got, c.access)
			}
			wantAllowExec := c.access == "exec"
			if got := vm["allow_exec"]; got != wantAllowExec {
				t.Errorf("allow_exec = %v, want %v (agent_access %q)", got, wantAllowExec, c.access)
			}
		})
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
