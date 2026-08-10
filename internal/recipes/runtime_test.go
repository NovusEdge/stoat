package recipes

import (
	"strings"
	"testing"
)

func TestBootstrapScriptShIsEmpty(t *testing.T) {
	if got := BootstrapScript("sh", "alpine"); got != "" {
		t.Errorf("BootstrapScript(sh, alpine) = %q, want empty", got)
	}
}

func TestBootstrapScriptPython3Alpine(t *testing.T) {
	got := BootstrapScript("python3", "alpine")
	for _, want := range []string{"command -v python3", "apk --wait 60 add python3"} {
		if !strings.Contains(got, want) {
			t.Errorf("BootstrapScript(python3, alpine) missing %q, got:\n%s", want, got)
		}
	}
}

func TestBootstrapScriptPython3Arch(t *testing.T) {
	got := BootstrapScript("python3", "arch")
	for _, want := range []string{"command -v python3", "pacman -S --noconfirm python"} {
		if !strings.Contains(got, want) {
			t.Errorf("BootstrapScript(python3, arch) missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "add python3") {
		t.Errorf("BootstrapScript(python3, arch) installed python3, want the arch package name python")
	}
}

func TestInterpreterArgs(t *testing.T) {
	if got := InterpreterArgs("sh"); len(got) != 2 || got[0] != "sh" || got[1] != "-s" {
		t.Errorf("InterpreterArgs(sh) = %v, want [sh -s]", got)
	}
	if got := InterpreterArgs("python3"); len(got) != 2 || got[0] != "python3" || got[1] != "-" {
		t.Errorf("InterpreterArgs(python3) = %v, want [python3 -]", got)
	}
}
