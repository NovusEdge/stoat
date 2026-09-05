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

func TestPreludeDefinesCmdVerbs(t *testing.T) {
	for _, name := range []string{"alpine", "debian", "ubuntu", "fedora", "arch"} {
		t.Run(name, func(t *testing.T) {
			o, ok := Lookup(name)
			if !ok {
				t.Fatalf("no guest %q", name)
			}
			got := Prelude(o, "sh")
			for _, fn := range []string{"stoat_download()", "stoat_useradd()"} {
				if !strings.Contains(got, fn) {
					t.Errorf("prelude does not define %s:\n%s", fn, got)
				}
			}
		})
	}
}

// {name} becomes "$1"; a template with no placeholder gets "$@". This is
// the same rule [svc] follows, so a recipe author learns it once.
func TestPreludeCmdTemplateRules(t *testing.T) {
	o := OS{
		Name: "freebsd", Init: "rc", Shell: "/bin/sh",
		Cmd: map[string]string{
			"download": "fetch -o",
			"useradd":  "pw useradd -n {name} -m",
		},
	}
	got := Prelude(o, "sh")
	if !strings.Contains(got, `stoat_download() { fetch -o "$@"; }`) {
		t.Errorf("download verb:\n%s", got)
	}
	if !strings.Contains(got, `stoat_useradd() { pw useradd -n "$1" -m; }`) {
		t.Errorf("useradd verb:\n%s", got)
	}
}

// The python prelude defines the same names over subprocess.run.
func TestPythonPreludeDefinesCmdVerbs(t *testing.T) {
	o, ok := Lookup("debian")
	if !ok {
		t.Fatal("bundled debian missing")
	}
	got := Prelude(o, "python3")
	for _, fn := range []string{"def stoat_download(", "def stoat_useradd("} {
		if !strings.Contains(got, fn) {
			t.Errorf("python prelude does not define %s:\n%s", fn, got)
		}
	}
}

func TestPythonPreludeCmdForwardsDownloadArguments(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("python3 is required to execute the public Python prelude")
	}
	dir := t.TempDir()
	recorded := filepath.Join(dir, "args")
	recorder := filepath.Join(dir, "record")
	if err := os.WriteFile(recorder, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RECORD\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(recorder, 0o755); err != nil {
		t.Fatal(err)
	}
	o := OS{
		Name: "freebsd", Init: "rc", Shell: "/bin/sh",
		Pkg: Pkg{Install: []string{"true"}},
		Cmd: map[string]string{"download": "record"},
	}
	body := Prelude(o, "python3") + "\nstoat_download(\"output.bin\", \"https://example.test/a\")\n"
	cmd := exec.Command("python3", "-c", body)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RECORD="+recorded)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python prelude failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "output.bin\nhttps://example.test/a\n" {
		t.Errorf("download args = %q, want output then URL", got)
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
