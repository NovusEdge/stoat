package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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

	newM, _ := m.updateDetail(keyMsg("i"))
	got := newM.(model)

	if v.Installed != false {
		t.Fatalf("v.Installed changed to %v in memory despite Save failing; want unchanged (false)", v.Installed)
	}
	if got.toast.text == "" || !got.toast.err {
		t.Fatalf("expected an error toast reporting the Save failure, got %+v", got.toast)
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

// TestTypeConsolePasswordKeyOnlyOfferedWhenAvailable proves the footer only
// advertises "t" (type console password into guest) when the VM actually has
// one to send — a stopped VM or one with no console password set must not
// show it, since pressing it then can never succeed.
func TestTypeConsolePasswordKeyOnlyOfferedWhenAvailable(t *testing.T) {
	cases := []struct {
		name  string
		vm    *config.VM
		shows bool
	}{
		{"stopped, has password", &config.VM{Name: "a", Mode: "cloud", ConsolePassword: "stoat", Dir: t.TempDir()}, false},
		{"running (fake), no password", &config.VM{Name: "b", Mode: "cloud", Dir: t.TempDir()}, false},
	}
	for _, c := range cases {
		m := model{screen: screenDetail, width: 100, height: 40, showHelp: true}
		m.detail = newDetail(c.vm)
		out := ansi.Strip(m.viewDetail())
		got := strings.Contains(out, "type password") || strings.Contains(out, "type console password")
		if got != c.shows {
			t.Errorf("%s: footer shows type-password key = %v, want %v", c.name, got, c.shows)
		}
	}
}

// TestTypeConsolePasswordKeyRefusesWhenUnavailable proves "t" reports a clear
// toast instead of silently doing nothing (or attempting to dial a monitor
// socket that cannot exist) when the VM is stopped or has no console
// password.
func TestTypeConsolePasswordKeyRefusesWhenUnavailable(t *testing.T) {
	v := &config.VM{Name: "stopped-vm", Mode: "cloud", ConsolePassword: "stoat", Dir: t.TempDir()}
	m := model{screen: screenDetail, detail: detailModel{vm: v}}

	newM, cmd := m.updateDetail(keyMsg("t"))
	got := newM.(model)

	if got.toast.text == "" || !got.toast.err {
		t.Fatalf("expected an error toast refusing to type the password, got %+v (cmd nil=%v)", got.toast, cmd == nil)
	}
}

// A cloud VM never gets a qemu window (qemu.NeedsWindow), so the detail
// screen must surface the VNC socket as the actual way to get a display --
// before this fix nothing anywhere told the user that socket exists, and
// the console-password row claimed a "(qemu window only)" that never
// appears for a VM this password is ever set on (it's only written for the
// cloudinit backend, which is always cloud mode). See IMPORTANT 3 in the
// final review.
func TestDetailSurfacesVNCForAHeadlessVM(t *testing.T) {
	v := &config.VM{Name: "cloudy", Mode: "cloud", ConsolePassword: "stoat", Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "vnc") {
		t.Fatalf("headless VM's detail screen must show a vnc row:\n%s", out)
	}
	if !strings.Contains(out, v.VNCPath()) {
		t.Fatalf("vnc row must show the actual socket path %q:\n%s", v.VNCPath(), out)
	}
	if strings.Contains(out, "qemu window only") {
		t.Errorf("cloud VMs never get a qemu window; the console row must not claim one:\n%s", out)
	}
}

// The one case that DOES get a real qemu window is an uninstalled disk VM
// (qemu.NeedsWindow). It should not additionally advertise a VNC row that
// implies the display lives at a socket instead.
func TestDetailOmitsVNCForAWindowedVM(t *testing.T) {
	v := &config.VM{Name: "installing", Mode: "disk", Installed: false, Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if strings.Contains(out, "vnc") {
		t.Errorf("a windowed VM (qemu window shows) must not also claim a vnc row:\n%s", out)
	}
}

// TestCopyConsolePasswordKeyOnlyOfferedWhenAvailable mirrors the "t" case:
// the footer must not advertise "c" (copy to clipboard) for a VM that has no
// console password to copy.
func TestCopyConsolePasswordKeyOnlyOfferedWhenAvailable(t *testing.T) {
	v := &config.VM{Name: "b", Mode: "cloud", Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40, showHelp: true}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())
	if strings.Contains(out, "copy password") || strings.Contains(out, "copy console password") {
		t.Errorf("footer shows copy-password key for a VM with no console password")
	}
}

// TestCopyConsolePasswordKeyRefusesWhenUnavailable proves "c" reports a clear
// toast, and issues no clipboard command, when there is no console password
// to copy.
func TestCopyConsolePasswordKeyRefusesWhenUnavailable(t *testing.T) {
	v := &config.VM{Name: "stopped-vm", Mode: "cloud", Dir: t.TempDir()}
	m := model{screen: screenDetail, detail: detailModel{vm: v}}

	newM, _ := m.updateDetail(keyMsg("c"))
	got := newM.(model)

	if got.toast.text == "" || !got.toast.err {
		t.Fatalf("expected an error toast refusing to copy, got %+v", got.toast)
	}
	if strings.Contains(got.toast.text, "stoat") {
		t.Fatalf("toast must not contain the password itself: %q", got.toast.text)
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

// TestInstallerHintMatchesOS covers the reported bug where an uninstalled
// disk VM was always told to run setup-alpine, even when its image was a
// Fedora/Debian/unknown BYO ISO that has no such command.
func TestInstallerHintMatchesOS(t *testing.T) {
	cases := []struct {
		os   string
		want string
	}{
		{"alpine", "setup-alpine"},
		{"fedora", "the installer"},
		{"", "the installer"},
	}
	for _, c := range cases {
		m := model{screen: screenDetail, width: 100, height: 40}
		m.detail = newDetail(&config.VM{
			Name: "d", Mode: "disk", OS: c.os, Dir: t.TempDir(), Disk: "disk.qcow2",
		})
		out := m.viewDetail()
		if !strings.Contains(out, c.want) {
			t.Errorf("os=%q: detail view missing %q", c.os, c.want)
		}
		if c.os != "alpine" && strings.Contains(out, "setup-alpine") {
			t.Errorf("os=%q: detail view still says setup-alpine", c.os)
		}
	}
}
