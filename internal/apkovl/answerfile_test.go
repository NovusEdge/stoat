package apkovl

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

const testKey = "ssh-ed25519 AAAAC3Nz stoat"

func TestGenerateAnswerfileSetsHostnameFromVMName(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v, testKey)
	if !strings.Contains(out, `HOSTNAMEOPTS="-n disky"`) {
		t.Errorf("answerfile does not set hostname from VM name, got:\n%s", out)
	}
}

func TestGenerateAnswerfileHasDiskopts(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v, testKey)
	if !strings.Contains(out, `DISKOPTS="-m sys /dev/vda"`) {
		t.Error("answerfile missing DISKOPTS: unattended install has nowhere to write")
	}
}

// TestGenerateAnswerfileSuppressesEveryPrompt pins the fields that keep
// setup-alpine from stalling on an interactive prompt an unattended run cannot
// answer. USEROPTS=none skips the user prompt; ROOTSSHKEY skips the sshd
// root-key prompt; ERASE_DISKS skips the disk-erase confirmation.
func TestGenerateAnswerfileSuppressesEveryPrompt(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v, testKey)
	for _, field := range []string{
		`USEROPTS="none"`,
		`ROOTSSHKEY="ssh-ed25519 AAAAC3Nz stoat"`,
		`ERASE_DISKS="/dev/vda"`,
	} {
		if !strings.Contains(out, field) {
			t.Errorf("answerfile missing prompt-suppressing field %q:\n%s", field, out)
		}
	}
}

func TestGenerateAnswerfileHasAllRequiredFields(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v, testKey)
	for _, field := range []string{
		"KEYMAPOPTS=", "HOSTNAMEOPTS=", "INTERFACESOPTS=", "DNSOPTS=",
		"TIMEZONEOPTS=", "PROXYOPTS=", "APKREPOSOPTS=", "SSHDOPTS=",
		"NTPOPTS=", "DISKOPTS=", "LBUOPTS=", "APKCACHEOPTS=",
	} {
		if !strings.Contains(out, field) {
			t.Errorf("answerfile missing required field %s", field)
		}
	}
}
