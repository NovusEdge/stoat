//go:build !linux

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostDiagnosticsRunWithoutDataRootMutation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"help"},
		{"version"},
		{"--json", "help"},
		{"--json", "version"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "stoat")
			t.Setenv("STOAT_HOME", root)
			var out, errOut bytes.Buffer
			if code := Main(args, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
				t.Fatalf("Main(%v) exit = %d, want ExitOK; stdout=%q stderr=%q", args, code, out.String(), errOut.String())
			}
			if out.Len() == 0 {
				t.Fatalf("Main(%v) produced no diagnostic output", args)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("Main(%v) created STOAT_HOME %q; stat err = %v", args, root, err)
			}
		})
	}

	root := filepath.Join(t.TempDir(), "doctor-root")
	t.Setenv("STOAT_HOME", root)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor"}, "test", strings.NewReader(""), &out, &errOut)
	if code != ExitFail {
		t.Fatalf("doctor exit = %d, want ExitFail for an unqualified host; stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	text := strings.ToLower(out.String())
	if !strings.Contains(text, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("doctor output does not identify the host: %q", out.String())
	}
	if !strings.Contains(text, "unsupported") && !strings.Contains(text, "unavailable") && !strings.Contains(text, "qualified") {
		t.Fatalf("doctor output does not describe the native host as unsupported or unavailable: %q", out.String())
	}
	for _, linuxAssumption := range []string{"/dev/kvm", "qemu-system-x86_64", "pacman", "apt", "dnf"} {
		if strings.Contains(text, linuxAssumption) {
			t.Errorf("doctor output suggests Linux dependency %q on %s: %q", linuxAssumption, runtime.GOOS, out.String())
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor created STOAT_HOME %q; stat err = %v", root, err)
	}
}

func TestUnsupportedCLICommandsNoMutation(t *testing.T) {
	t.Chdir(t.TempDir())
	commands := [][]string{
		{"create", "work", "--image", "alpine.iso", "--secret", "docker.authkey"},
		{"pull", "alpine"},
		{"up", "work"},
		{"down", "work"},
		{"apply", "work"},
		{"ssh", "work"},
		{"exec", "work", "true"},
		{"cp", "/tmp/host", "work:/tmp/guest"},
		{"snapshot", "work", "checkpoint"},
		{"rm", "-y", "work"},
		{"init"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "stoat")
			t.Setenv("STOAT_HOME", root)
			var out, errOut bytes.Buffer
			code := Main(args, "test", strings.NewReader(""), &out, &errOut)
			if code != ExitFail {
				t.Fatalf("Main(%v) exit = %d, want ExitFail; stdout=%q stderr=%q", args, code, out.String(), errOut.String())
			}
			if strings.Contains(errOut.String(), "STOAT_SECRET_") {
				t.Fatalf("Main(%v) resolved a secret before the unsupported-host guard: %q", args, errOut.String())
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("Main(%v) created STOAT_HOME %q; stat err = %v", args, root, err)
			}
			if _, err := os.Stat("stoat.toml"); !os.IsNotExist(err) {
				t.Fatalf("Main(%v) created project file before refusing: stat err = %v", args, err)
			}
		})
	}
}
