package apkovl

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestGenerateAnswerfileSetsHostnameFromVMName(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v)
	if !strings.Contains(out, `HOSTNAMEOPTS="-n disky"`) {
		t.Errorf("answerfile does not set hostname from VM name, got:\n%s", out)
	}
}

func TestGenerateAnswerfileHasDiskopts(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v)
	if !strings.Contains(out, `DISKOPTS="-m sys /dev/vda"`) {
		t.Error("answerfile missing DISKOPTS: unattended install has nowhere to write")
	}
}

func TestGenerateAnswerfileHasAllRequiredFields(t *testing.T) {
	v := &config.VM{Name: "disky"}
	out := GenerateAnswerfile(v)
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
