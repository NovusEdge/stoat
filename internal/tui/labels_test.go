package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestRecipeLabel covers the display-only rename. VM.Recipes still stores the
// filename, and recipes.Read opens it, so this must never be fed back into a
// config; it exists because "xfce.alpine.sh" told the user about a per-OS
// suffix the picker had already filtered on.
func TestRecipeLabel(t *testing.T) {
	cases := []struct{ file, want string }{
		{"xfce.alpine.sh", "xfce"},
		{"xfce.ubuntu.sh", "xfce"},
		{"xfce.cloud.yaml", "xfce"},
		{"xfce.fedora.cloud.yaml", "xfce"},
		{"docker.arch.sh", "docker"},
		{"plain.sh", "plain"},
		{"noextension", "noextension"},
		{"", ""},
	}
	for _, c := range cases {
		if got := recipeLabel(c.file); got != c.want {
			t.Errorf("recipeLabel(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

// TestModeHintsFitThePane guards the hint text against the pane width. A hint
// longer than the value column wraps, which pushes every row below it down and
// makes the form jump as the user cycles modes.
func TestModeHintsFitThePane(t *testing.T) {
	const indent = 11 // the form's value column
	for _, mode := range []string{"live", "disk", "cloud"} {
		h := modeHint(mode)
		if h == "" {
			t.Errorf("mode %q has no hint", mode)
			continue
		}
		if n := indent + len([]rune(h)); n > formContentWidth {
			t.Errorf("hint for %q is %d cells wide, pane content is %d: %q",
				mode, n, formContentWidth, h)
		}
	}
	if modeHint("nonsense") != "" {
		t.Error("an unknown mode should have no hint rather than a wrong one")
	}
}

// TestDiskModeHintExplainsTheInstallStep is the reason this exists at all: a
// user created an 8G disk VM, pressed p, and waited 90 seconds for "ssh not
// reachable" because nothing said a disk VM needs its OS installed by hand.
func TestDiskModeHintExplainsTheInstallStep(t *testing.T) {
	h := modeHint("disk")
	if !strings.Contains(h, "install") {
		t.Errorf("disk hint doesn't mention installing: %q", h)
	}

	// And the detail pane repeats it for a VM that already exists.
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(&config.VM{
		Name: "d", Mode: "disk", OS: "alpine", ISO: "isos/a.iso",
		RAM: 1024, CPUs: 1, Disk: "8G", SSHPort: 2200, Dir: t.TempDir(),
	})
	if !strings.Contains(m.viewDetail(), h) {
		t.Error("the detail pane doesn't show the mode hint")
	}
}

// TestWrapItemsKeepsRowsInsideThePane covers the recipes row growing with the
// catalog: at four entries the inline list ran past the pane and wrapped
// mid-item, leaving a bare "xfce" on its own line under the label column.
func TestWrapItemsKeepsRowsInsideThePane(t *testing.T) {
	const width = formContentWidth - 11
	indent := strings.Repeat(" ", 11)

	items := []string{"[x] devtools", "[ ] docker", "[x] tailscale", "[ ] xfce"}
	out := wrapItems(items, width, indent)

	for i, line := range strings.Split(out, "\n") {
		got := len([]rune(strings.TrimRight(line, " ")))
		if i > 0 {
			// Continuation lines carry the indent, so they're measured whole.
			if !strings.HasPrefix(line, indent) {
				t.Errorf("continuation line %d is not aligned under the first: %q", i, line)
			}
			got = len([]rune(strings.TrimRight(line, " ")))
			if got > width+len(indent) {
				t.Errorf("line %d is %d cells, max %d: %q", i, got, width+len(indent), line)
			}
			continue
		}
		if got > width {
			t.Errorf("line %d is %d cells, max %d: %q", i, got, width, line)
		}
	}

	// Every item must survive the wrap. Losing one silently would hide a
	// recipe the user could otherwise select.
	for _, it := range items {
		if !strings.Contains(out, it) {
			t.Errorf("wrapItems dropped %q", it)
		}
	}

	if wrapItems(nil, width, indent) != "" {
		t.Error("no items should render as nothing, not a blank line")
	}
	if got := wrapItems([]string{"[ ] one"}, width, indent); got != "[ ] one" {
		t.Errorf("a single item should not be wrapped or indented: %q", got)
	}
}
