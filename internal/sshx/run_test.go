package sshx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
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

// alpineEscalate is the first word of alpine's escalate argv, read from the
// guest definition rather than spelled here, so a guest file that switches
// its escalate command does not silently pass this test.
func alpineEscalate(t *testing.T) string {
	t.Helper()
	o, ok := guest.Lookup("alpine")
	if !ok || len(o.Escalate) == 0 {
		t.Fatal("alpine's guest definition has no escalate argv")
	}
	return o.Escalate[0]
}

func TestRunEscalatesOnlyWhenAsked(t *testing.T) {
	calls := testutil.FakeSSH(t, `true`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "stoat", OS: "alpine"}
	esc := alpineEscalate(t)

	if _, _, _, err := sshx.Run(context.Background(), v, false, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls.Calls()[0].Remote, esc) {
		t.Fatalf("a tool escalated on its own: %q", calls.Calls()[0].Remote)
	}
	if _, _, _, err := sshx.Run(context.Background(), v, true, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls.Calls()[1].Remote, esc) {
		t.Fatalf("root=true did not apply alpine's escalate: %q", calls.Calls()[1].Remote)
	}
}

func TestRunDoesNotEscalateForRoot(t *testing.T) {
	calls := testutil.FakeSSH(t, `true`)
	v := &config.VM{Name: "work", SSHPort: 2222, SSHUser: "root", OS: "alpine"}
	if _, _, _, err := sshx.Run(context.Background(), v, true, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	// The fake ssh logs its whole argv space joined, so a suffix check on
	// "'id'" alone would also pass an escalated "'sudo' '-n' 'id'": both
	// strings end in "'id'". Comparing the whole line against Args with the
	// bare quoted argv is the only check that catches a prefix Run should
	// not have added.
	want := strings.Join(sshx.Args(v, sshx.Quote([]string{"id"})), " ")
	if got := calls.Calls()[0].Remote; got != want {
		t.Fatalf("ssh argv = %q, want %q (root must not escalate)", got, want)
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
