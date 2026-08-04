package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/testutil"
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
// one to send. A stopped VM or one with no console password set must not
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
// screen must surface the VNC socket as the actual way to get a display.
// Before this fix nothing anywhere told the user that socket exists, and
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
// It gets that window only where there is a graphical session to open it on,
// so the override is pinned rather than left to whatever host runs the test.
func TestDetailOmitsVNCForAWindowedVM(t *testing.T) {
	t.Setenv(qemu.GraphicalEnv, "1")
	v := &config.VM{Name: "installing", Mode: "disk", Installed: false, Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if strings.Contains(out, "vnc") {
		t.Errorf("a windowed VM (qemu window shows) must not also claim a vnc row:\n%s", out)
	}
}

// The same VM on a host with no graphical session: there is no window to be
// had, so the detail screen shows the socket AND says why, since "no window"
// is exactly what the user came here confused about.
func TestDetailExplainsTheVNCFallbackOnAHeadlessHost(t *testing.T) {
	t.Setenv(qemu.GraphicalEnv, "0")
	fakeViewerPath(t, "gvncviewer")
	v := &config.VM{Name: "installing", Mode: "disk", Installed: false, Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 120, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, v.VNCPath()) {
		t.Errorf("the install console is on the socket now; the detail screen must show it:\n%s", out)
	}
	if !strings.Contains(out, "no usable graphical session on this host") {
		t.Errorf("detail screen does not say why there is no window:\n%s", out)
	}
}

// A socket path alone was not enough. The reported failure is a disk VM whose
// window disappears the moment setup-alpine marks it installed, by a user left
// holding a path and no idea what opens it, so the detail pane names a viewer
// that is actually on this host.
func TestDetailShowsHowToAttachToTheVNCSocket(t *testing.T) {
	fakeViewerPath(t, "gvncviewer")
	v := &config.VM{Name: "alpinedisk", Mode: "disk", Installed: true, Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 120, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "gvncviewer") {
		t.Errorf("detail pane does not say what opens the socket:\n%s", out)
	}
}

// Naming a viewer the user does not have is worse than naming none: it reads
// as an instruction and fails as one.
func TestDetailSaysWhatToInstallWhenNoViewerExists(t *testing.T) {
	fakeViewerPath(t)
	v := &config.VM{Name: "alpinedisk", Mode: "disk", Installed: true, Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 120, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "no VNC viewer found") {
		t.Errorf("detail pane must admit nothing is installed:\n%s", out)
	}
}

// fakeViewerPath makes PATH hold exactly the named binaries, so what the
// detail pane offers does not depend on what this host has installed.
func fakeViewerPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
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

// TestDetailShowsForwards proves a VM's declared port forwards are rendered
// on the detail screen. Before this, core.VM.Forwards had no reader anywhere
// in the TUI (the migration plan's D1), which is how the edit screen was
// able to assign a VM's ssh port to a host port another VM had already
// forwarded without anyone noticing.
func TestDetailShowsForwards(t *testing.T) {
	v := &config.VM{
		Name: "fwd-vm", Mode: "live", Dir: t.TempDir(),
		Forwards: []config.PortForward{{HostPort: 8080, GuestPort: 80}},
	}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "8080") || !strings.Contains(out, "80") {
		t.Fatalf("detail view missing the declared forward 8080->80:\n%s", out)
	}
}

// TestDetailOmitsForwardsRowWhenNone proves a VM with no declared forwards
// renders without a "forward" row at all, rather than an empty one: the
// same convention every other optional row on this screen (iso, share,
// recipes, …) already follows.
func TestDetailOmitsForwardsRowWhenNone(t *testing.T) {
	v := &config.VM{Name: "no-fwd", Mode: "live", Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if strings.Contains(out, "forward") {
		t.Fatalf("VM with no forwards must not render a forward row:\n%s", out)
	}
	// Also catches a stray active/next-start caption rendered on its own
	// (facts.row with an empty label), which the "forward" check above
	// would miss since that line never contains the word "forward" itself.
	if strings.Contains(out, "in effect") || strings.Contains(out, "next start") {
		t.Fatalf("VM with no forwards must not render an effect caption either:\n%s", out)
	}
}

// TestDetailShowsRecipeStatus proves the recipes row reports each recipe's
// applied state individually: pending for one never run, applied (with the
// date) for one recorded in v.Applied, and stale for an entry left in
// v.Applied whose recipe was since removed from v.Recipes.
func TestDetailShowsRecipeStatus(t *testing.T) {
	appliedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	v := &config.VM{
		Name:    "recipe-vm",
		Mode:    "live",
		Dir:     t.TempDir(),
		Recipes: []string{"xfce.alpine.sh", "docker.alpine.sh"},
		Applied: map[string]config.AppliedRecipe{
			"xfce.alpine.sh":    {Version: "1", At: appliedAt},
			"removed.alpine.sh": {Version: "1", At: appliedAt},
		},
	}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "xfce (applied 2026-01-02)") {
		t.Errorf("applied recipe must show its applied date:\n%s", out)
	}
	if !strings.Contains(out, "docker (pending)") {
		t.Errorf("recipe never applied must show pending:\n%s", out)
	}
	if !strings.Contains(out, "removed (stale - removed from config)") {
		t.Errorf("applied recipe no longer in v.Recipes must show stale:\n%s", out)
	}
}

// TestDetailForwardsDistinguishRunningFromStopped proves the rendering never
// collapses "in effect" and "applies at next start" into the same text: a
// running VM's forwards must read differently from a stopped VM's, because
// qemu cannot hot-add a hostfwd rule to a live process (docs/design/core-
// api.md §8 decision 5). Running is faked the same way the rest of this
// package's tests do: qemu.Running keys off the pidfile at v.Dir, not a
// live process, so writing one is enough.
func TestDetailForwardsDistinguishRunningFromStopped(t *testing.T) {
	fwds := []config.PortForward{{HostPort: 8080, GuestPort: 80}}

	stopped := &config.VM{Name: "stopped-fwd", Mode: "live", Dir: t.TempDir(), Forwards: fwds}
	mStopped := model{screen: screenDetail, width: 100, height: 40}
	mStopped.detail = newDetail(stopped)
	outStopped := ansi.Strip(mStopped.viewDetail())

	running := &config.VM{Name: "running-fwd", Mode: "live", Dir: t.TempDir(), Forwards: fwds}
	stop := testutil.FakeRunning(t, running.Dir)
	defer stop()
	mRunning := model{screen: screenDetail, width: 100, height: 40}
	mRunning.detail = newDetail(running)
	outRunning := ansi.Strip(mRunning.viewDetail())

	if !strings.Contains(outStopped, "in effect") {
		t.Fatalf("stopped VM's forwards must say they are in effect:\n%s", outStopped)
	}
	if strings.Contains(outStopped, "next start") {
		t.Fatalf("stopped VM's forwards must not also claim a next-start caveat:\n%s", outStopped)
	}
	if !strings.Contains(outRunning, "next start") {
		t.Fatalf("running VM's forwards must say they apply at next start, not now:\n%s", outRunning)
	}
	if strings.Contains(outRunning, "in effect") {
		t.Fatalf("running VM's forwards must not also claim to be in effect now:\n%s", outRunning)
	}
}

// TestLogPagerOpensAndEscCloses proves "L" opens the console log pager (via
// its logOpenedMsg round trip, since the read happens in a Cmd off the UI
// goroutine) and esc closes it again, handing the detail screen's normal
// body back.
func TestLogPagerOpensAndEscCloses(t *testing.T) {
	// core.Logs (which openLogPager calls) resolves the VM by name under
	// config.Root(), not by v.Dir, so the fixture has to live there: a
	// t.TempDir() VM with no matching config.Root() entry is "not found" to
	// it regardless of what v.Dir points at.
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{Name: "pager-vm", Mode: "live"}
	if err := v.Save(); err != nil {
		t.Fatalf("saving fixture vm.toml: %v", err)
	}
	if err := os.WriteFile(v.ConsoleLogPath(), []byte("boot line one\nboot line two\n"), 0o644); err != nil {
		t.Fatalf("writing console.log: %v", err)
	}

	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)

	newM, cmd := m.updateDetail(keyMsg("L"))
	got := newM.(model)
	if got.detail.pager != nil {
		t.Fatalf("pager should not open synchronously; opening reads the log off the UI goroutine")
	}
	if cmd == nil {
		t.Fatalf("expected a Cmd to open the pager")
	}
	msg := cmd()
	opened, ok := msg.(logOpenedMsg)
	if !ok {
		t.Fatalf("expected logOpenedMsg, got %T (%v)", msg, msg)
	}

	newM, _ = got.updateDetail(opened)
	got = newM.(model)
	if got.detail.pager == nil {
		t.Fatalf("logOpenedMsg should have installed the pager")
	}

	out := ansi.Strip(got.viewDetail())
	if !strings.Contains(out, "boot line two") {
		t.Fatalf("pager view missing the console log content:\n%s", out)
	}
	if !strings.Contains(out, "esc close") {
		t.Fatalf("pager view missing its esc-close hint:\n%s", out)
	}

	newM, _ = got.updateDetail(keyMsg("esc"))
	got = newM.(model)
	if got.detail.pager != nil {
		t.Fatalf("esc should have closed the pager")
	}
	// Back to the ordinary detail body, not stuck on the pager's view.
	out = ansi.Strip(got.viewDetail())
	if strings.Contains(out, "boot line two") {
		t.Fatalf("closed pager must not still be rendering the log:\n%s", out)
	}
}

// TestLogPagerEscTakesPriorityOverDetailBindings proves that while the pager
// is open, esc closes IT rather than falling through to the detail screen's
// own esc/back binding, which would otherwise leave the pager's viewport
// state dangling on a screen the user has already left.
func TestLogPagerEscTakesPriorityOverDetailBindings(t *testing.T) {
	v := &config.VM{Name: "pager-vm-2", Mode: "live", Dir: t.TempDir()}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	m.detail.pager = &logPager{}

	newM, _ := m.updateDetail(keyMsg("esc"))
	got := newM.(model)
	if got.screen != screenDetail {
		t.Fatalf("esc with the pager open must not also navigate back to the list; screen=%v", got.screen)
	}
	if got.detail.pager != nil {
		t.Fatalf("esc must close the pager")
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
