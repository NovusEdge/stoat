package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/core"
)

func provFixture(t *testing.T, log string) core.VM {
	t.Helper()
	dir := t.TempDir()
	applyLog := filepath.Join(dir, "last-provision.log")
	if log != "" {
		if err := os.WriteFile(applyLog, []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return core.VM{
		Name: "work", Mode: "live", OS: "alpine", SSHPort: 2200,
		Paths: core.Paths{Dir: dir, ApplyLog: applyLog},
	}
}

// TestReadProvStep covers what the spinner line reports, across the stages a
// real run passes through. The step comes from the log the run already
// writes, so there is no progress channel to keep in sync. There is no
// percentage either, because nothing knows how many packages a recipe will
// install.
func TestReadProvStep(t *testing.T) {
	cases := []struct {
		name, log, wantStep, wantLast string
	}{
		{"no log yet", "", "starting", ""},
		{
			"waiting for ssh",
			"waiting for ssh on port 2200…\n",
			"waiting for ssh", "",
		},
		{
			"inside a recipe",
			"waiting for ssh on port 2200…\n\n=== recipe xfce.alpine.sh ===\n(147/263) Installing gtk+3.0\n",
			"xfce.alpine.sh", "(147/263) Installing gtk+3.0",
		},
		{
			"second recipe wins",
			"=== recipe a.alpine.sh ===\nout a\n=== recipe b.alpine.sh ===\nout b\n",
			"b.alpine.sh", "out b",
		},
		{
			"finished",
			"=== recipe xfce.alpine.sh ===\nOK: 672.4 MiB in 378 packages\n\ndone\n",
			"xfce.alpine.sh", "done",
		},
		{
			"failed",
			"waiting for ssh on port 2200…\nFAILED: work: ssh not reachable on port 2200 after 1m30s\n",
			"waiting for ssh", "FAILED: work: ssh not reachable on port 2200 after 1m30s",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			step, last := readProvStep(provFixture(t, c.log))
			if step != c.wantStep {
				t.Errorf("step = %q, want %q", step, c.wantStep)
			}
			if last != c.wantLast {
				t.Errorf("last = %q, want %q", last, c.wantLast)
			}
		})
	}
}

// TestReadProvStepReadsOnlyTheTail proves the log is not re-read whole
// several times a second. A desktop install writes hundreds of KB, and only
// the end of it means "current".
func TestReadProvStepReadsOnlyTheTail(t *testing.T) {
	big := strings.Repeat("(1/263) Installing something-or-other\n", 8000) // ~300KB
	v := provFixture(t, "=== recipe xfce.alpine.sh ===\n"+big+"OK: done installing\n")

	step, last := readProvStep(v)
	if last != "OK: done installing" {
		t.Errorf("last = %q, want the final line", last)
	}
	// The recipe marker is now far outside the tail window, so the step falls
	// back rather than reporting a stale recipe name.
	if step == "" {
		t.Error("step should never be empty")
	}
	if n := len(tailBytes(v.Paths.ApplyLog, provTailBytes)); int64(n) > provTailBytes {
		t.Errorf("read %d bytes, want at most %d", n, provTailBytes)
	}
}

func TestProvElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{900 * time.Millisecond, "1s"},
		{45 * time.Second, "45s"},
		{96 * time.Second, "1m36s"},
		{214 * time.Second, "3m34s"},
		{time.Hour + 5*time.Second, "60m05s"},
	}
	for _, c := range cases {
		if got := provElapsed(c.d); got != c.want {
			t.Errorf("provElapsed(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestProvisionRefusedUntilInstalled is the regression test for a reported
// failure: pressing "p" on a disk VM whose OS had never been installed sat
// for the full 90s ssh timeout, then said "ssh not reachable", which says
// nothing about the real cause. apkovl installs the key and enables sshd,
// and it is built for LIVE mode only. A disk VM has no ssh until the guest
// OS is installed.
func TestProvisionRefusedUntilInstalled(t *testing.T) {
	m := model{provisioning: map[string]provState{}, spin: newSpinner()}
	v := core.VM{
		Name: "alpine-test-1", Mode: "disk", OS: "alpine", Installed: false,
		SSHPort: 2201, Recipes: []string{"xfce.alpine.sh"},
		Paths: core.Paths{Dir: t.TempDir()},
	}

	// A refusal still returns a Cmd (the one that shows its toast), so the
	// invariant to check is that no run was registered, not that no Cmd came
	// back.
	m.startProvision(v)
	if len(m.provisioning) != 0 {
		t.Error("the VM was marked as provisioning anyway")
	}
	for _, want := range []string{"not installed", "setup-alpine", "stop and start"} {
		if !strings.Contains(m.toast.text, want) {
			t.Errorf("toast = %q, missing %q", m.toast.text, want)
		}
	}

	// Once marked installed it proceeds (and fails later on ssh, honestly).
	v.Installed = true
	if cmd := m.startProvision(v); cmd == nil {
		t.Error("an installed disk VM was still refused")
	}

	// A non-Alpine disk VM must not be told to run setup-alpine.
	m2 := model{provisioning: map[string]provState{}, spin: newSpinner()}
	m2.startProvision(core.VM{
		Name: "fed", Mode: "disk", OS: "fedora", SSHPort: 2202,
		Paths: core.Paths{Dir: t.TempDir()}, Recipes: []string{"x.sh"},
	})
	if strings.Contains(m2.toast.text, "setup-alpine") {
		t.Errorf("a fedora VM was told to run setup-alpine: %q", m2.toast.text)
	}
}
