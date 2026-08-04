package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
)

// TestAccessBoxSaysHowToGetIn covers the whole point of the panel: the answer
// differs per VM, and guessing wrong leaves you at a login prompt.
func TestAccessBoxSaysHowToGetIn(t *testing.T) {
	cases := []struct {
		name   string
		vm     *config.VM
		want   []string
		reject []string
	}{
		{
			"cloud with a console password",
			&config.VM{Name: "u", Mode: "cloud", SSHPort: 2201, SSHUser: "stoat", ConsolePassword: "stoat"},
			[]string{"stoat@127.0.0.1:2201", "stoat / stoat", "console-only"},
			nil,
		},
		{
			"live alpine",
			&config.VM{Name: "l", Mode: "live", SSHPort: 2200},
			[]string{"root@127.0.0.1:2200", "no password"},
			[]string{"console-only"}, // the ssh/console caveat is cloud-specific
		},
		{
			"cloud with no password (created before that existed)",
			&config.VM{Name: "old", Mode: "cloud", SSHPort: 2203, SSHUser: "stoat"},
			[]string{"locked", "ssh only"},
			nil,
		},
		{
			"disk vm",
			&config.VM{Name: "d", Mode: "disk", SSHPort: 2204},
			[]string{"set during install"},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := accessBox(c.vm, "", newCloudInitProgress(), 200)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("access box missing %q:\n%s", w, out)
				}
			}
			for _, r := range c.reject {
				if strings.Contains(out, r) {
					t.Errorf("access box should not contain %q:\n%s", r, out)
				}
			}
		})
	}

	if accessBox(nil, "", newCloudInitProgress(), 200) != "" {
		t.Error("no VM selected should render no box at all")
	}
}

// TestAccessBoxNeverWraps is the reported problem: a long key path wrapped
// mid-token and read as two broken lines. Values are truncated instead.
func TestAccessBoxNeverWraps(t *testing.T) {
	v := &config.VM{Name: "u", Mode: "cloud", SSHPort: 65535, SSHUser: "stoat", ConsolePassword: "stoat"}
	out := accessBox(v, "running", newCloudInitProgress(), 200)

	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > accessWidth+2*paneBorderAndPadding {
			t.Errorf("line is %d cells, box is %d: %q", w, accessWidth, line)
		}
	}
}

// TestAccessBoxDroppedOnNarrowTerminals: the list pane and this box side by
// side need real width, and overlapping them is worse than not showing it.
func TestAccessBoxDroppedOnNarrowTerminals(t *testing.T) {
	pane := "PANE"
	access := "ACCESS"

	if got := joinAccess(pane, access, accessMinTerminal-1); strings.Contains(got, access) {
		t.Error("the access box was rendered on a terminal too narrow for it")
	}
	if got := joinAccess(pane, access, accessMinTerminal); !strings.Contains(got, access) {
		t.Error("the access box was dropped on a terminal wide enough for it")
	}
	if got := joinAccess(pane, "", 200); got != pane {
		t.Errorf("an empty access box changed the pane: %q", got)
	}
}

// TestCloudInitStates pins the mapping from cloud-init's own vocabulary to
// what it means for the user, and which states stop the poll loop. Getting
// "done" wrong would poll a finished VM over ssh forever.
func TestCloudInitStates(t *testing.T) {
	cases := []struct {
		status   string
		done     bool
		fraction float64
		label    string
	}{
		{"waiting", false, 0.15, "booting"},
		{"running", false, 0.6, "installing"},
		{"done", true, 1, "ready"},
		{"error", true, 1, "failed"},
		{"disabled", true, 1, "disabled"},
		{"not-run", true, 1, "not-run"},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			if got := cloudInitDone(c.status); got != c.done {
				t.Errorf("cloudInitDone = %v, want %v", got, c.done)
			}
			if got := cloudInitFraction(c.status); got != c.fraction {
				t.Errorf("cloudInitFraction = %v, want %v", got, c.fraction)
			}
			if l := cloudInitLabel(c.status); !strings.Contains(l, c.label) {
				t.Errorf("cloudInitLabel = %q, want it to mention %q", l, c.label)
			}
		})
	}

	// An unknown status must not claim the run finished, since that would stop
	// the poll and leave the UI reporting a stage that never advances.
	if cloudInitDone("wat") {
		t.Error("an unrecognised status was treated as terminal")
	}
	if cloudInitLabel("") != "" {
		t.Error("no status should render nothing rather than a placeholder")
	}
}
