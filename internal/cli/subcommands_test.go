package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// saveVM writes a minimal stopped VM into the current test data root.
func saveVM(t *testing.T, v *config.VM) *config.VM {
	t.Helper()
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	return v
}

func dataOf(t *testing.T, objs []map[string]any) map[string]any {
	t.Helper()
	d, _ := result(t, objs)["data"].(map[string]any)
	if d == nil {
		t.Fatalf("result has no data object: %v", objs)
	}
	return d
}

func TestGetReturnsTheVM(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "work", OS: "alpine", Mode: "live", RAM: 1024, CPUs: 2, SSHPort: 2200})

	code, objs := runJSON(t, "get", "work")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	vm, _ := dataOf(t, objs)["vm"].(map[string]any)
	if vm == nil {
		t.Fatal("data.vm missing")
	}
	if vm["name"] != "work" || vm["os"] != "alpine" {
		t.Errorf("vm = %v", vm)
	}
	// Forwards is empty here, and it must still be a list.
	if _, ok := vm["forwards"].([]any); !ok {
		t.Errorf("forwards = %#v, want an empty array", vm["forwards"])
	}
}

func TestGetHumanReadable(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200})

	var out, errOut strings.Builder
	if code := Main([]string{"get", "work"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "name: work") {
		t.Errorf("output missing the name line: %q", out.String())
	}
}

func TestGetMissingVMIsNotFound(t *testing.T) {
	cliRoot(t)
	code, objs := runJSON(t, "get", "ghost")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeNotFound) {
		t.Errorf("code = %v, want %q", errObj["code"], wire.CodeNotFound)
	}
}

// ssh-command exists so a caller can run ssh itself, which --json refuses to
// do on its behalf. The argv is only useful if it is complete.
func TestSSHCommandReturnsARunnableArgv(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "work", OS: "alpine", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, SSHUser: "root"})

	code, objs := runJSON(t, "ssh-command", "work")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	argv, _ := dataOf(t, objs)["argv"].([]any)
	if len(argv) == 0 {
		t.Fatal("argv is empty")
	}
	if argv[0] != "ssh" {
		t.Errorf("argv[0] = %v, want ssh", argv[0])
	}
	joined := ""
	for _, a := range argv {
		s, _ := a.(string)
		joined += s + " "
	}
	if !strings.Contains(joined, "2200") {
		t.Errorf("argv does not carry the ssh port: %v", argv)
	}
}

// logs <vm> on a VM that has never been started: core.Logs serves an empty
// reader rather than an error, and the empty result must be [] not null.
func TestLogsVMWithNoLogFile(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200})

	code, objs := runJSON(t, "logs", "work")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	data := dataOf(t, objs)
	if data["vm"] != "work" || data["which"] != "console" {
		t.Errorf("vm/which = %v/%v", data["vm"], data["which"])
	}
	if lines, ok := data["lines"].([]any); !ok || len(lines) != 0 {
		t.Errorf("lines = %#v, want an empty array", data["lines"])
	}
}

func TestLogsVMTailsAndSelectsWhich(t *testing.T) {
	dir := cliRoot(t)
	v := saveVM(t, &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200})
	v.Dir = filepath.Join(dir, "work")
	if err := os.WriteFile(v.ProvisionLogPath(), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, objs := runJSON(t, "logs", "work", "--which", "apply", "-n", "2")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	data := dataOf(t, objs)
	if data["which"] != "apply" {
		t.Errorf("which = %v, want apply", data["which"])
	}
	lines, _ := data["lines"].([]any)
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Errorf("lines = %v, want the last two", lines)
	}
}

// Both public VM log selectors pass through the same redaction boundary. Keep
// this at the CLI sink so a correct core reader cannot be bypassed by JSON
// line handling or tailing.
func TestLogsJSONRedactsStoredSecretsForConsoleAndApply(t *testing.T) {
	dir := cliRoot(t)
	v := saveVM(t, &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200})
	const sentinel = "cli-logs-secret-sentinel"
	if err := config.SaveSecrets(v.Dir, config.Secrets{"docker": {"authkey": sentinel}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work", "console.log"), []byte("console "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work", "last-provision.log"), []byte("apply "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, which := range []core.Which{core.WhichConsole, core.WhichApply} {
		t.Run(string(which), func(t *testing.T) {
			code, objs := runJSON(t, "logs", "work", "--which", string(which))
			if code != ExitOK {
				t.Fatalf("exit = %d: %v", code, objs)
			}
			lines, _ := dataOf(t, objs)["lines"].([]any)
			if len(lines) != 1 {
				t.Fatalf("lines = %#v, want one redacted line", lines)
			}
			line, _ := lines[0].(string)
			if strings.Contains(line, sentinel) {
				t.Fatalf("logs %s leak stored secret %q: %q", which, sentinel, line)
			}
			if !strings.Contains(line, "<redacted>") {
				t.Errorf("logs %s = %q, want the redaction marker", which, line)
			}
			if _, err := json.Marshal(objs); err != nil {
				t.Fatalf("logs %s result is not JSON-marshalable: %v", which, err)
			}
		})
	}
}

// The CLI must preserve the secret-store refusal and identify the VM rather
// than falling back to raw console bytes when a reader is insecure.
func TestLogsJSONPropagatesInsecureSecretStoreWithVMContext(t *testing.T) {
	cliRoot(t)
	v := saveVM(t, &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200})
	path := filepath.Join(v.Dir, config.SecretsName)
	if err := os.WriteFile(path, []byte("docker.authkey = \"sentinel\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	code, objs := runJSON(t, "logs", "work", "--which", "console")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d: %v", code, ExitFail, objs)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	message, _ := errObj["message"].(string)
	if !strings.Contains(message, "work") || !strings.Contains(message, "secrets.toml: mode 0644") {
		t.Errorf("error.message = %q, want VM context and secret-file mode", message)
	}
}

// The no-name form must keep tailing stoat's own log, not become a usage
// error now that the positional exists.
func TestLogsWithNoVMStillTailsStoatsOwnLog(t *testing.T) {
	cliRoot(t)
	code, objs := runJSON(t, "logs")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	data := dataOf(t, objs)
	if _, ok := data["lines"].([]any); !ok {
		t.Errorf("lines = %#v, want an array", data["lines"])
	}
	if _, leaked := data["vm"]; leaked {
		t.Errorf("no vm was named, but data carries one: %v", data)
	}
}

// Main installs the embedded recipe set into the temp root on every run, so
// these assert against the real shipped catalog rather than a fixture.
func TestRecipesListsAndFilters(t *testing.T) {
	cliRoot(t)

	_, all := runJSON(t, "recipes")
	every, _ := dataOf(t, all)["recipes"].([]any)
	if len(every) == 0 {
		t.Fatal("recipes returned nothing with no filter")
	}

	// debian satisfies xfce, devtools, docker, python-dev, build-deps, service-tools, and tailscale OS lists.
	_, only := runJSON(t, "recipes", "--os", "debian", "--backend", "cloudinit")
	debian, _ := dataOf(t, only)["recipes"].([]any)
	if len(debian) == 0 {
		t.Fatal("recipes --os debian --backend cloudinit returned nothing")
	}
	want := map[string]bool{"xfce": true, "devtools": true, "docker": true, "python-dev": true, "tailscale": true, "build-deps": true, "service-tools": true}
	for _, r := range debian {
		m, _ := r.(map[string]any)
		name, _ := m["name"].(string)
		if !want[name] {
			t.Errorf("debian/cloudinit offered unexpected recipe %q", name)
		}
	}
	if len(debian) != len(want) {
		t.Errorf("debian/cloudinit offered %d recipes, want %d", len(debian), len(want))
	}
}

func TestRecipesUnknownOSIsAnEmptyArray(t *testing.T) {
	cliRoot(t)
	code, objs := runJSON(t, "recipes", "--os", "plan9", "--backend", "apkovl")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	rs, ok := dataOf(t, objs)["recipes"].([]any)
	if !ok {
		t.Fatalf("recipes = %#v, want an array", dataOf(t, objs)["recipes"])
	}
	if len(rs) != 0 {
		t.Errorf("recipes = %v, want empty", rs)
	}
}

// check-recipes exits 0 in both directions: it succeeded at checking, and an
// inapplicable recipe is the answer rather than a failure to produce one.
func TestCheckRecipesApplicableAndNot(t *testing.T) {
	cliRoot(t)

	code, objs := runJSON(t, "check-recipes", "xfce", "--os", "alpine", "--backend", "apkovl")
	if code != ExitOK {
		t.Fatalf("applicable: exit = %d: %v", code, objs)
	}
	data := dataOf(t, objs)
	if data["applicable"] != true {
		t.Errorf("applicable = %v, want true", data["applicable"])
	}
	if issues, _ := data["issues"].([]any); len(issues) != 0 {
		t.Errorf("issues = %v, want empty", issues)
	}

	// A non-existent recipe is inapplicable to any OS.
	code, objs = runJSON(t, "check-recipes", "nosuchrecipe", "--os", "debian", "--backend", "cloudinit")
	if code != ExitOK {
		t.Fatalf("inapplicable: exit = %d, want 0: checking SUCCEEDED", code)
	}
	res := result(t, objs)
	if res["ok"] != true {
		t.Errorf("ok = %v, want true: an inapplicable recipe is an answer", res["ok"])
	}
	data = dataOf(t, objs)
	if data["applicable"] != false {
		t.Errorf("applicable = %v, want false", data["applicable"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("issues is empty for an inapplicable recipe")
	}
	first, _ := issues[0].(map[string]any)
	if first["name"] == "" || first["reason"] == "" {
		t.Errorf("issue = %v, want a name and a reason", first)
	}
}

func TestParseCheckRecipesUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"check-recipes", "--os", "alpine"}, // no names
		{"check-recipes", "xfce"},           // no --os
		{"check-recipes"},                   // neither
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) accepted, want a usage error", args)
		}
	}
}

func TestParseApplyOnly(t *testing.T) {
	a, err := Parse([]string{"apply", "work", "--only", "a.sh, b.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Only) != 2 || a.Only[0] != "a.sh" || a.Only[1] != "b.sh" {
		t.Errorf("Only = %v, want [a.sh b.sh]", a.Only)
	}
}

func TestApplyMissingVMIsNotFound(t *testing.T) {
	cliRoot(t)
	code, objs := runJSON(t, "apply", "ghost")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeNotFound) {
		t.Errorf("code = %v, want %q", errObj["code"], wire.CodeNotFound)
	}
}

// A cloudinit VM applies over ssh after first boot (item 3), so apply no
// longer refuses it. With no recipes there is nothing to run, so apply is a
// clean no-op rather than the old applied_at_boot refusal.
func TestApplyOnACloudVMIsNoLongerRefused(t *testing.T) {
	dir := cliRoot(t)
	v := saveVM(t, &config.VM{
		Name: "cloudy", OS: "ubuntu", Mode: "cloud", Backend: "cloudinit",
		RAM: 2048, CPUs: 2, SSHPort: 2201,
	})
	v.Dir = filepath.Join(dir, "cloudy")
	stop := fakeRunning(t, v)
	defer stop()

	code, objs := runJSON(t, "apply", "cloudy")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: %v", code, ExitOK, objs)
	}
}

// jsonLogWriter is the piece that keeps provision and apply output from
// corrupting the JSON Lines stream, and the piece most likely to be quietly
// wrong: bytes arrive in whatever chunks the file grew by, not in lines.
func TestJSONLogWriterSplitsOnLinesNotWrites(t *testing.T) {
	var buf strings.Builder
	w := &jsonLogWriter{em: wire.NewEmitter(&buf), cmd: "apply"}

	// One line split across three writes, then two lines in one write.
	for _, chunk := range []string{"hel", "lo wor", "ld\nsecond\nthird\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(buf.String(), "\n"); got != 3 {
		t.Errorf("emitted %d events, want 3: %q", got, buf.String())
	}
	if !strings.Contains(buf.String(), `"hello world"`) {
		t.Errorf("a line split across writes was not reassembled: %q", buf.String())
	}

	// A trailing line with no newline is held until Flush. A recipe killed
	// mid-write leaves exactly that, and it is the line that says why.
	before := buf.String()
	if _, err := w.Write([]byte("no newline yet")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != before {
		t.Error("a partial line was emitted before Flush")
	}
	w.Flush()
	if !strings.Contains(buf.String(), `"no newline yet"`) {
		t.Errorf("Flush did not emit the trailing line: %q", buf.String())
	}
}

// The recipe boundary markers sshx.Provision writes become stage events, so a
// consumer gets real per-recipe progress instead of an invented percentage.
func TestJSONLogWriterEmitsAStageAtEachRecipeMarker(t *testing.T) {
	var buf strings.Builder
	w := &jsonLogWriter{em: wire.NewEmitter(&buf), cmd: "apply"}
	if _, err := w.Write([]byte("=== recipe xfce ===\ninstalling\n")); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"type":"stage"`) || !strings.Contains(out, `"recipe":"xfce"`) {
		t.Errorf("no stage event for the marker: %q", out)
	}
	if strings.Count(out, `"type":"log"`) != 2 {
		t.Errorf("want the marker still carried as a log line too: %q", out)
	}
}

// core.Logs serves a broken VM's console log from its directory alone, and
// that is exactly the VM whose log someone needs.
func TestLogsWorksOnABrokenVM(t *testing.T) {
	dir := cliRoot(t)
	writeBrokenVMToml(t, dir, "wrecked")
	logPath := filepath.Join(dir, "wrecked", "console.log")
	if err := os.WriteFile(logPath, []byte("boot failed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "logs", "wrecked", "--which", string(core.WhichConsole))
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: a broken VM's log is the one diagnostic left: %v", code, objs)
	}
	lines, _ := dataOf(t, objs)["lines"].([]any)
	if len(lines) != 1 || lines[0] != "boot failed" {
		t.Errorf("lines = %v", lines)
	}
}
