package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
)

// TestTickGenerationOnlyReArmsCurrentChain proves the fix for the ticker
// chain leak: a tickMsg carrying a stale generation must not re-arm (it
// falls through and its chain dies), while one carrying the current
// generation does re-arm. Without the generation check, updateDetail
// re-armed any tickMsg as long as m.screen == screenDetail, regardless of
// which visit to the detail screen scheduled it, so every esc->right cycle
// left an extra self-perpetuating chain running forever.
func TestTickGenerationOnlyReArmsCurrentChain(t *testing.T) {
	v := &config.VM{Name: "gen-test", Mode: "live", Dir: t.TempDir()}

	cases := []struct {
		name      string
		detailGen int
		msgGen    int
		screen    screen
		wantArmed bool
	}{
		{"current generation, on detail screen: re-arms", 2, 2, screenDetail, true},
		{"stale generation, on detail screen: does not re-arm", 2, 1, screenDetail, false},
		{"even-more-stale generation: does not re-arm", 5, 1, screenDetail, false},
		{"current generation but left the detail screen: does not re-arm", 2, 2, screenList, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := model{
				screen:    tc.screen,
				detailGen: tc.detailGen,
				detail:    detailModel{vm: v},
			}
			_, cmd := m.updateDetail(tickMsg{t: time.Now(), gen: tc.msgGen})
			armed := cmd != nil
			if armed != tc.wantArmed {
				t.Fatalf("gen=%d msgGen=%d screen=%v: armed=%v, want %v",
					tc.detailGen, tc.msgGen, tc.screen, armed, tc.wantArmed)
			}
		})
	}
}

// TestRapidReentryLeavesOneLiveGeneration simulates entering the detail
// screen 5 times in rapid succession (as list.go's "right"/"l" case does:
// bump detailGen, schedule tick(detailGen)) and confirms only the tick
// carrying the final generation re-arms; every earlier chain is stale by
// construction and dies on arrival.
func TestRapidReentryLeavesOneLiveGeneration(t *testing.T) {
	v := &config.VM{Name: "gen-test", Mode: "live", Dir: t.TempDir()}
	m := model{screen: screenDetail, detail: detailModel{vm: v}}

	var scheduled []int
	for i := 0; i < 5; i++ {
		m.detailGen++
		scheduled = append(scheduled, m.detailGen) // mirrors tick(m.detailGen)
	}

	live := 0
	for _, gen := range scheduled {
		_, cmd := m.updateDetail(tickMsg{t: time.Now(), gen: gen})
		if cmd != nil {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("expected exactly one live chain to re-arm after 5 rapid entries, got %d", live)
	}
	if scheduled[len(scheduled)-1] != m.detailGen {
		t.Fatalf("final scheduled generation %d does not match m.detailGen %d", scheduled[len(scheduled)-1], m.detailGen)
	}
}

// TestToggleInstalledFailedSaveLeavesMemoryUnchanged proves the fix for the
// "i" toggle: on a Save failure, the in-memory VM's Installed field must
// stay exactly as it was on disk, not flip and stick despite the write
// never landing. Forces the failure by making vm.toml read-only, which a
// non-root user cannot write to regardless of directory permissions.
func TestToggleInstalledFailedSaveLeavesMemoryUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced, cannot force a Save failure this way")
	}

	dir := t.TempDir()
	v := &config.VM{
		Name:      "readonly-test",
		Mode:      "disk",
		Disk:      "8G",
		Installed: false,
		Dir:       dir,
	}
	if err := v.Save(); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	tomlPath := filepath.Join(dir, "vm.toml")
	if err := os.Chmod(tomlPath, 0o444); err != nil {
		t.Fatalf("chmod vm.toml: %v", err)
	}
	t.Cleanup(func() { os.Chmod(tomlPath, 0o644) }) // let TempDir cleanup remove it

	m := model{
		screen:    screenDetail,
		detailGen: 1,
		detail:    detailModel{vm: v},
	}

	newM, _ := m.updateDetail(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	got := newM.(model)

	if v.Installed != false {
		t.Fatalf("v.Installed changed to %v in memory despite Save failing; want unchanged (false)", v.Installed)
	}
	if got.status == "" {
		t.Fatal("expected a non-empty status message reporting the Save error")
	}

	// Confirm the toggle truly never touched disk.
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("reading vm.toml back: %v", err)
	}
	if got, want := string(b), "installed = false"; !contains(got, want) {
		t.Fatalf("vm.toml on disk does not contain %q:\n%s", want, got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
