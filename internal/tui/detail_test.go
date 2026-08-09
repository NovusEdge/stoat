package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/qemu"
)

// containsWrapped reports whether s contains needle once the pane's border
// and whitespace are stripped. The facts pane holds every value to a fixed
// column width now, so a path longer than that column wraps mid-token
// across two rendered lines instead of pushing the pane wider.
func containsWrapped(s, needle string) bool {
	flat := strings.NewReplacer("\n", "", " ", "", "│", "").Replace(s)
	return strings.Contains(flat, needle)
}

// TestTickGenerationOnlyReArmsCurrentChain proves the fix for the ticker
// chain leak. A tickMsg carrying a stale generation must not re-arm; its
// chain dies. One carrying the current generation must re-arm. Without the
// generation check, updateDetail re-armed any tickMsg as long as
// m.screen == screenDetail, regardless of which visit scheduled it. Every
// esc-then-right cycle left an extra chain running forever.
func TestTickGenerationOnlyReArmsCurrentChain(t *testing.T) {
	v := core.VM{Name: "gen-test", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}

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
// screen 5 times in rapid succession, as list.go's "right"/"l" case does:
// bump detailGen, schedule tick(detailGen). Only the tick carrying the final
// generation must re-arm. Every earlier chain is stale by construction and
// dies on arrival.
func TestRapidReentryLeavesOneLiveGeneration(t *testing.T) {
	v := core.VM{Name: "gen-test", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}
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
// "i" toggle. On a write failure, the in-memory VM's Installed field must
// stay exactly as it was on disk. It must not flip and stick when the write
// never lands. This also proves the "i" key goes through core.Update, which
// takes the data-root lock, instead of the second unlocked Save() it used to
// call straight on vm.toml. It forces the failure by making vm.toml
// read-only, which a non-root user cannot write to regardless of directory
// permissions.
func TestToggleInstalledFailedSaveLeavesMemoryUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced, cannot force a Save failure this way")
	}
	t.Setenv("STOAT_HOME", t.TempDir())

	cv := &config.VM{Name: "readonly-test", Mode: "disk", Disk: "8G", Installed: false}
	if err := cv.Save(); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	tomlPath := filepath.Join(cv.Dir, "vm.toml")
	if err := os.Chmod(tomlPath, 0o444); err != nil {
		t.Fatalf("chmod vm.toml: %v", err)
	}
	t.Cleanup(func() { os.Chmod(tomlPath, 0o644) }) // let TempDir cleanup remove it

	v, err := core.Get(cv.Name)
	if err != nil {
		t.Fatalf("core.Get: %v", err)
	}
	m := model{
		screen:    screenDetail,
		detailGen: 1,
		detail:    detailModel{vm: v},
	}

	newM, _ := m.updateDetail(keyMsg("i"))
	got := newM.(model)

	if got.detail.vm.Installed != false {
		t.Fatalf("detail.vm.Installed changed to %v in memory despite the write failing; want unchanged (false)", got.detail.vm.Installed)
	}
	if got.toast.text == "" || !got.toast.err {
		t.Fatalf("expected an error toast reporting the write failure, got %+v", got.toast)
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

// TestDisplayKeyCyclesAndPersists proves "d" cycles auto -> window -> vnc ->
// auto, saves each step through core.Update, and toasts the new value.
func TestDisplayKeyCyclesAndPersists(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	cv := &config.VM{Name: "cycle-test", Mode: "cloud"}
	if err := cv.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	v, err := core.Get(cv.Name)
	if err != nil {
		t.Fatal(err)
	}
	m := model{screen: screenDetail, detail: detailModel{vm: v}}

	for _, want := range []string{"window", "vnc", "auto"} {
		newM, _ := m.updateDetail(keyMsg("d"))
		m = newM.(model)
		if m.detail.vm.Display != wantDisplay(want) {
			t.Fatalf("Display = %q, want %q", m.detail.vm.Display, wantDisplay(want))
		}
		if !strings.Contains(m.toast.text, "display: "+want) {
			t.Fatalf("toast = %q, want it to mention display: %s", m.toast.text, want)
		}
		if m.toast.err {
			t.Fatalf("toast marked as error for a successful cycle: %+v", m.toast)
		}
		reloaded, err := config.Load(cv.Name)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Display != wantDisplay(want) {
			t.Fatalf("not persisted: Display = %q, want %q", reloaded.Display, wantDisplay(want))
		}
	}
}

// wantDisplay maps a cycle step's label to the value core.Patch actually
// writes: "auto" round-trips to "", config.VM.Display's own default.
func wantDisplay(label string) string {
	if label == "auto" {
		return ""
	}
	return label
}

// TestTypeConsolePasswordKeyOnlyOfferedWhenAvailable proves the footer
// advertises "t" (type console password into guest) only when the VM has
// one to send. A stopped VM, or one with no console password set, must not
// show it. Pressing it then could never succeed.
func TestTypeConsolePasswordKeyOnlyOfferedWhenAvailable(t *testing.T) {
	cases := []struct {
		name  string
		vm    core.VM
		shows bool
	}{
		{"stopped, has password", core.VM{Name: "a", Mode: "cloud", ConsolePassword: "stoat", State: core.StateStopped}, false},
		{"running, no password", core.VM{Name: "b", Mode: "cloud", State: core.StateRunning}, false},
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

// TestTypeConsolePasswordKeyRefusesWhenUnavailable proves "t" reports a
// clear toast instead of doing nothing silently. It also must not attempt to
// dial a monitor socket that cannot exist, when the VM is stopped or has no
// console password.
func TestTypeConsolePasswordKeyRefusesWhenUnavailable(t *testing.T) {
	v := core.VM{Name: "stopped-vm", Mode: "cloud", ConsolePassword: "stoat", State: core.StateStopped}
	m := model{screen: screenDetail, detail: detailModel{vm: v}}

	newM, cmd := m.updateDetail(keyMsg("t"))
	got := newM.(model)

	if got.toast.text == "" || !got.toast.err {
		t.Fatalf("expected an error toast refusing to type the password, got %+v (cmd nil=%v)", got.toast, cmd == nil)
	}
}

// A cloud VM never gets a qemu window (qemu.NeedsWindow), so the detail
// screen must surface the VNC socket as the actual way to get a display.
// Before this fix, nothing told the user that socket exists. The
// console-password row also claimed a "(qemu window only)" that never
// applies: the password is set only by the cloudinit backend, which is
// always cloud mode.
func TestDetailSurfacesVNCForAHeadlessVM(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "vnc.sock")
	v := core.VM{Name: "cloudy", Mode: "cloud", ConsolePassword: "stoat", Paths: core.Paths{Dir: dir, VNCSocket: sock}}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "vnc") {
		t.Fatalf("headless VM's detail screen must show a vnc row:\n%s", out)
	}
	if !containsWrapped(out, sock) {
		t.Fatalf("vnc row must show the actual socket path %q:\n%s", sock, out)
	}
	if strings.Contains(out, "qemu window only") {
		t.Errorf("cloud VMs never get a qemu window; the console row must not claim one:\n%s", out)
	}
}

// The one case that gets a real qemu window is an uninstalled disk VM
// (qemu.NeedsWindow). It must not also advertise a VNC row that implies the
// display lives at a socket. It gets that window only when a graphical
// session exists to open it on, so the test pins the override instead of
// relying on the host running it.
func TestDetailOmitsVNCForAWindowedVM(t *testing.T) {
	t.Setenv(qemu.GraphicalEnv, "1")
	v := core.VM{Name: "installing", Mode: "disk", Installed: false, Paths: core.Paths{Dir: t.TempDir()}}
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
	dir := t.TempDir()
	sock := filepath.Join(dir, "vnc.sock")
	v := core.VM{Name: "installing", Mode: "disk", Installed: false, Paths: core.Paths{Dir: dir, VNCSocket: sock}}
	m := model{screen: screenDetail, width: 120, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !containsWrapped(out, sock) {
		t.Errorf("the install console is on the socket now; the detail screen must show it:\n%s", out)
	}
	if !strings.Contains(out, "no usable graphical session on this host") {
		t.Errorf("detail screen does not say why there is no window:\n%s", out)
	}
}

// A socket path alone was not enough. A disk VM's window disappears the
// moment setup-alpine marks it installed, leaving the user with a path and
// no way to open it. The detail pane instead names a viewer actually
// installed on this host.
func TestDetailShowsHowToAttachToTheVNCSocket(t *testing.T) {
	fakeViewerPath(t, "gvncviewer")
	v := core.VM{Name: "alpinedisk", Mode: "disk", Installed: true, Paths: core.Paths{Dir: t.TempDir()}}
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
	v := core.VM{Name: "alpinedisk", Mode: "disk", Installed: true, Paths: core.Paths{Dir: t.TempDir()}}
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
	v := core.VM{Name: "b", Mode: "cloud", Paths: core.Paths{Dir: t.TempDir()}}
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
	v := core.VM{Name: "stopped-vm", Mode: "cloud", Paths: core.Paths{Dir: t.TempDir()}}
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

// TestDetailShowsForwards proves a VM's declared port forwards render on the
// detail screen. Before this, core.VM.Forwards had no reader anywhere in the
// TUI (the migration plan's D1). That gap let the edit screen assign a VM's
// ssh port to a host port another VM had already forwarded, unnoticed.
func TestDetailShowsForwards(t *testing.T) {
	v := core.VM{
		Name: "fwd-vm", Mode: "live", Paths: core.Paths{Dir: t.TempDir()},
		Forwards: []core.PortForward{{HostPort: 8080, GuestPort: 80}},
	}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if !strings.Contains(out, "8080") || !strings.Contains(out, "80") {
		t.Fatalf("detail view missing the declared forward 8080->80:\n%s", out)
	}
}

// TestDetailOmitsForwardsRowWhenNone proves a VM with no declared forwards
// renders without a "forward" row at all, not an empty one. Every other
// optional row on this screen (iso, share, recipes, …) follows the same
// convention.
func TestDetailOmitsForwardsRowWhenNone(t *testing.T) {
	v := core.VM{Name: "no-fwd", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	out := ansi.Strip(m.viewDetail())

	if strings.Contains(out, "forward") {
		t.Fatalf("VM with no forwards must not render a forward row:\n%s", out)
	}
	// This also catches a stray active/next-start caption rendered on its
	// own (facts.row with an empty label). The "forward" check above would
	// miss it: that line never contains the word "forward".
	if strings.Contains(out, "in effect") || strings.Contains(out, "next start") {
		t.Fatalf("VM with no forwards must not render an effect caption either:\n%s", out)
	}
}

// TestDetailShowsRecipeStatus proves the recipes row reports each recipe's
// applied state individually. A recipe never run shows pending. One
// recorded in v.Applied shows applied, with the date. An entry in v.Applied
// whose recipe was since removed from v.Recipes shows stale.
func TestDetailShowsRecipeStatus(t *testing.T) {
	appliedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	v := core.VM{
		Name:    "recipe-vm",
		Mode:    "live",
		Paths:   core.Paths{Dir: t.TempDir()},
		Recipes: []string{"xfce.alpine.sh", "docker.alpine.sh"},
		Applied: map[string]core.AppliedRecipe{
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
// collapses "in effect" and "applies at next start" into the same text. A
// running VM's forwards must read differently from a stopped VM's: qemu
// cannot hot-add a hostfwd rule to a live process (docs/design/core-
// api.md §8 decision 5). Running is set directly on core.VM.State, not
// faked via a pidfile. core.Get already resolves that question, via
// qemu.Running, by the time a caller has a core.VM. viewDetail reads that
// resolved State instead of re-deriving it from the filesystem.
func TestDetailForwardsDistinguishRunningFromStopped(t *testing.T) {
	fwds := []core.PortForward{{HostPort: 8080, GuestPort: 80}}

	stopped := core.VM{Name: "stopped-fwd", Mode: "live", State: core.StateStopped, Forwards: fwds}
	mStopped := model{screen: screenDetail, width: 100, height: 40}
	mStopped.detail = newDetail(stopped)
	outStopped := ansi.Strip(mStopped.viewDetail())

	running := core.VM{Name: "running-fwd", Mode: "live", State: core.StateRunning, Forwards: fwds}
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

// TestLogPagerOpensAndEscCloses proves "L" opens the console log pager. The
// read happens in a Cmd off the UI goroutine, via the logOpenedMsg round
// trip. esc closes the pager again and hands the detail screen's normal
// body back.
func TestLogPagerOpensAndEscCloses(t *testing.T) {
	// core.Logs, which openLogPager calls, resolves the VM by name under
	// config.Root(), not by v.Dir. The fixture has to live there. A
	// t.TempDir() VM with no matching config.Root() entry is "not found" to
	// it, regardless of what v.Dir points at.
	t.Setenv("STOAT_HOME", t.TempDir())
	cv := &config.VM{Name: "pager-vm", Mode: "live"}
	if err := cv.Save(); err != nil {
		t.Fatalf("saving fixture vm.toml: %v", err)
	}
	if err := os.WriteFile(cv.ConsoleLogPath(), []byte("boot line one\nboot line two\n"), 0o644); err != nil {
		t.Fatalf("writing console.log: %v", err)
	}
	v, err := core.Get(cv.Name)
	if err != nil {
		t.Fatalf("core.Get: %v", err)
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
// is open, esc closes it. It must not fall through to the detail screen's
// own esc/back binding. That fallthrough would leave the pager's viewport
// state dangling on a screen the user already left.
func TestLogPagerEscTakesPriorityOverDetailBindings(t *testing.T) {
	v := core.VM{Name: "pager-vm-2", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}
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

// TestInstallerHintMatchesOS covers the installed-field hint on an uninstalled
// disk VM. An Alpine VM installs itself unattended, so it says so. A
// Fedora/Debian/unknown BYO ISO has no setup-alpine, so it names its own
// installer instead.
func TestInstallerHintMatchesOS(t *testing.T) {
	cases := []struct {
		os      string
		backend string
		want    string
	}{
		{"alpine", "apkovl", "installing on first boot"},
		{"fedora", "cloudinit", "the installer"},
		{"", "", "the installer"},
	}
	for _, c := range cases {
		m := model{screen: screenDetail, width: 100, height: 40}
		m.detail = newDetail(core.VM{
			Name: "d", Mode: "disk", OS: c.os, Backend: c.backend, Paths: core.Paths{Dir: t.TempDir()}, Disk: "disk.qcow2",
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
