package sshx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
	"github.com/novusedge/stoat/internal/testutil"
)

func TestRunPassesArgvIntactThroughTheGuestShell(t *testing.T) {
	// The argv goes host argv -> ssh's trailing arguments -> the guest
	// shell's re-parse. A path with a space and a semicolon proves no layer
	// drops a word boundary or lets the guest shell read a separator.
	calls := testutil.FakeSSH(t, `printf '%s\n' "$@" | tail -n 1`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "root"}

	out, _, code, err := sshx.Run(context.Background(), v, false,
		[]string{"touch", "/tmp/a b;rm -rf /", "x"}, nil)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	remote := calls.Calls()[0].Remote
	if !strings.Contains(remote, `'/tmp/a b;rm -rf /'`) {
		t.Fatalf("remote command did not quote the path: %q", remote)
	}
	_ = out
}

func TestRunReportsANonZeroExitAsData(t *testing.T) {
	testutil.FakeSSH(t, `exit 42`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "root"}
	_, _, code, err := sshx.Run(context.Background(), v, false, []string{"false"}, nil)
	if err != nil {
		t.Fatalf("a non-zero guest exit was reported as an error: %v", err)
	}
	if code != 42 {
		t.Fatalf("code = %d, want 42", code)
	}
}

func TestRunEscalatesOnlyWhenAsked(t *testing.T) {
	calls := testutil.FakeSSH(t, `true`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "stoat", OS: "alpine"}

	if _, _, _, err := sshx.Run(context.Background(), v, false, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls.Calls()[0].Remote, "doas") || strings.Contains(calls.Calls()[0].Remote, "sudo") {
		t.Fatalf("a tool escalated on its own: %q", calls.Calls()[0].Remote)
	}
	if _, _, _, err := sshx.Run(context.Background(), v, true, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls.Calls()[1].Remote, "doas") {
		t.Fatalf("root=true did not apply alpine's escalate: %q", calls.Calls()[1].Remote)
	}
}

func TestRunDoesNotEscalateForRoot(t *testing.T) {
	calls := testutil.FakeSSH(t, `true`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "root", OS: "alpine"}
	if _, _, _, err := sshx.Run(context.Background(), v, true, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls.Calls()[0].Remote, "doas") {
		t.Fatalf("escalated for a root ssh user: %q", calls.Calls()[0].Remote)
	}
}

func TestRunSendsStdin(t *testing.T) {
	testutil.FakeSSH(t, `cat`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "root"}
	out, _, _, err := sshx.Run(context.Background(), v, false, []string{"cat"}, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello" {
		t.Fatalf("stdout = %q, want %q", out, "hello")
	}
}
