package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/project"
)

// The template stoat init writes must load in Reject mode. Otherwise the
// first command a new user runs after init fails on the file init made.
func TestInitOutputLoads(t *testing.T) {
	cliRoot(t)
	dir := t.TempDir()
	t.Chdir(dir)

	code, _ := runJSON(t, "init", "--name", "myrepo")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatalf("stoat init wrote a file it cannot read back: %v", err)
	}
	if p.Name != "myrepo" {
		t.Errorf("name = %q, want myrepo", p.Name)
	}
	if len(p.VMs) != 1 || p.VMs[0].Key != "dev" {
		t.Errorf("vms = %+v, want one, keyed dev", p.VMs)
	}
}

func TestInitDefaultsTheNameToTheDirectory(t *testing.T) {
	cliRoot(t)
	dir := filepath.Join(t.TempDir(), "myrepo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if code, _ := runJSON(t, "init"); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	body, err := os.ReadFile(filepath.Join(dir, project.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `name = "myrepo"`) {
		t.Errorf("stoat.toml does not name the directory:\n%s", body)
	}
}

// .stoat holds the recipe cache and the secrets file. Committing either is a
// mistake init prevents once, in a git checkout only.
func TestInitAppendsTheCacheDirToGitignore(t *testing.T) {
	cliRoot(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if code, _ := runJSON(t, "init"); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "bin/\n.stoat/\n" {
		t.Errorf(".gitignore = %q, want the cache dir appended once", got)
	}

	// A second init must not append it twice.
	runJSON(t, "init")
	body, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Count(string(body), ".stoat/") != 1 {
		t.Errorf(".gitignore = %q, want one .stoat/ line", body)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	cliRoot(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte("schema = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	code, objs := runJSON(t, "init")
	if code != ExitFail {
		t.Errorf("exit = %d, want %d", code, ExitFail)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "already exists") {
		t.Errorf("message = %q", msg)
	}
}
