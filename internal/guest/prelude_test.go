package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// sh functions. No module ships to the guest.
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

// STOAT_OUTPUT belongs to the per-recipe execution wrapper. The shared
// prelude also runs health and package-setup commands, which must not create
// or truncate a recipe output file.
func TestPreludeDoesNotInitializeStoatOutput(t *testing.T) {
	o, ok := Lookup("alpine")
	if !ok {
		t.Fatal("bundled alpine missing")
	}
	for _, runtime := range []string{"sh", "python3"} {
		got := Prelude(o, runtime)
		if strings.Contains(got, "STOAT_OUTPUT") || strings.Contains(got, "/tmp/.stoat-out") {
			t.Errorf("%s prelude initializes recipe output state:\n%s", runtime, got)
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

// Alpine's stoat_pkg_setup retries setup-apkrepos when the apk database is
// locked. A stub on PATH fails twice then succeeds; the real setup value
// sleeps 2s between attempts, so this exercises the retry without waiting on
// the full 30-attempt bound.
func TestAlpinePkgSetupRetriesOnLockedDatabase(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required")
	}
	o, ok := Lookup("alpine")
	if !ok {
		t.Fatal("bundled alpine missing")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "attempts")
	stub := filepath.Join(dir, "setup-apkrepos")
	script := "#!/bin/sh\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n + 1))\n" +
		"echo $n > " + counter + "\n" +
		"[ $n -gt 2 ] || exit 99\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	body := Prelude(o, "sh") + "stoat_pkg_setup\n"
	cmd := exec.Command("sh", "-c", body)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stoat_pkg_setup did not recover from a locked database: %v\n%s", err, out)
	}
	got, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "3" {
		t.Errorf("attempts = %s, want 3 (two failures then success)", got)
	}
}

// The python3 rendering runs stoat_pkg_setup through sh -c too (prelude.go's
// _run("sh", "-c", ...)), so the same retry loop must survive %q-escaping and
// python turning the escaped newlines back into real ones.
func TestAlpinePkgSetupRetriesOnLockedDatabasePython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required")
	}
	o, ok := Lookup("alpine")
	if !ok {
		t.Fatal("bundled alpine missing")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "attempts")
	stub := filepath.Join(dir, "setup-apkrepos")
	script := "#!/bin/sh\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n + 1))\n" +
		"echo $n > " + counter + "\n" +
		"[ $n -gt 2 ] || exit 99\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	body := Prelude(o, "python3") + "\nstoat_pkg_setup()\n"
	cmd := exec.Command("python3", "-c", body)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python stoat_pkg_setup did not recover from a locked database: %v\n%s", err, out)
	}
	got, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "3" {
		t.Errorf("attempts = %s, want 3 (two failures then success)", got)
	}
}

// The give-up branch exits non-zero instead of looping forever. The real
// alpine setup bounds at 30 attempts with a 2s sleep (~60s), too slow for a
// test; this uses a synthetic OS with the same loop shape and a bound of 2 to
// exercise the give-up branch in well under a second.
func TestAlpinePkgSetupGivesUpWhenDatabaseStaysLocked(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required")
	}
	o := OS{
		Name: "alpine", Init: "openrc", Shell: "/bin/ash",
		Pkg: Pkg{
			Setup: `n=0
until setup-apkrepos -c -1; do
    n=$((n + 1))
    [ "$n" -ge 2 ] && { echo "apk database stayed locked; giving up" >&2; exit 1; }
    sleep 0
done`,
			Install: []string{"apk", "add"},
		},
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "setup-apkrepos")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := Prelude(o, "sh") + "stoat_pkg_setup\n"
	cmd := exec.Command("sh", "-c", body)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("stoat_pkg_setup succeeded against a stub that always fails:\n%s", out)
	}
	if !strings.Contains(string(out), "giving up") {
		t.Errorf("output = %q, want a give-up message", out)
	}
}

// pacman reinstalls a package it already has unless --needed is passed.
// devtools and build-deps both install base-devel on Arch, so a VM that
// selects both would download the whole group twice.
func TestArchInstallSkipsSatisfiedPackages(t *testing.T) {
	o, ok := Lookup("arch")
	if !ok {
		t.Fatal("bundled arch missing")
	}
	if !slices.Contains(o.Pkg.Install, "--needed") {
		t.Errorf("arch install = %v, want --needed", o.Pkg.Install)
	}
	if !strings.Contains(o.Pkg.ScaffoldInstall, "--needed") {
		t.Errorf("arch scaffold_install = %q, want --needed", o.Pkg.ScaffoldInstall)
	}
}
