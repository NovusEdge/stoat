package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestQuietHumanVMRemoveRefusalExplainsHowToConfirm(t *testing.T) {
	cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"--quiet", "rm", "work"}, "test", nil, &out, &errOut)
	if code != ExitFail {
		t.Fatalf("quiet rm exit = %d, want ExitFail", code)
	}
	if out.Len() != 0 {
		t.Fatalf("quiet rm wrote stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "pass -y") {
		t.Fatalf("quiet rm refusal is not actionable: %q", errOut.String())
	}
	if _, err := config.Load("work"); err != nil {
		t.Fatalf("quiet refusal removed VM: %v", err)
	}
}
