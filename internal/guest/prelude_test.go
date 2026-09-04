package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every bundled guest's rendered sh prelude is a golden file and must parse
// as POSIX sh. UPDATE_GOLDEN=1 writes the golden from the current output;
// run it once when Prelude is implemented, then commit the files.
func TestPreludeGolden(t *testing.T) {
	for _, o := range All() {
		got := Prelude(o, "sh")
		path := filepath.Join("testdata", "prelude", o.Name+".sh")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with UPDATE_GOLDEN=1 once)", o.Name, err)
		}
		if string(want) != got {
			t.Errorf("%s prelude drifted from testdata; diff and update on purpose", o.Name)
		}
		if _, err := exec.LookPath("sh"); err == nil {
			cmd := exec.Command("sh", "-n")
			cmd.Stdin = strings.NewReader(got)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s prelude is not valid sh: %s", o.Name, out)
			}
		}
	}
}

// The python3 runtime gets a parallel prelude over subprocess.run instead of
// sh functions, per docs/specs/2026-09-04-guest-definitions-design.md's
// "Verbs in recipes" section. No module ships to the guest.
func TestPreludePython(t *testing.T) {
	o, ok := Lookup("arch")
	if !ok {
		t.Fatal("bundled arch missing")
	}
	got := Prelude(o, "python3")
	for _, want := range []string{
		"import os, subprocess",
		`os.environ["STOAT_OS"] = "arch"`,
		"def stoat_pkg_install(",
		"def stoat_svc_enable(",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// WithPrelude inserts after a leading shebang line so the interpreter line
// stays first; a body with no shebang gets the prelude in front.
func TestWithPreludeKeepsShebangFirst(t *testing.T) {
	got := WithPrelude("#!/bin/sh\nset -e\necho hi\n", "P\n")
	if !strings.HasPrefix(got, "#!/bin/sh\nP\nset -e\n") {
		t.Errorf("got:\n%s", got)
	}
	if got := WithPrelude("echo hi\n", "P\n"); got != "P\necho hi\n" {
		t.Errorf("no-shebang case: %q", got)
	}
}
