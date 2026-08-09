package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// fakeBins puts an executable file per name into a fresh directory and makes
// it the whole of PATH, so LookPath finds exactly these and nothing the host
// happens to have installed.
func fakeBins(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestDisplayKind(t *testing.T) {
	for _, c := range []struct {
		pref      string
		mode      string
		installed bool
		graphical bool
		want      string
	}{
		// pref "" and "auto" both resolve to a window on a graphical host,
		// for every mode and installed state.
		{"", "disk", false, true, DisplayWindow},
		{"auto", "disk", false, true, DisplayWindow},
		{"", "disk", true, true, DisplayWindow},
		{"", "live", false, true, DisplayWindow},
		{"", "live", true, true, DisplayWindow},
		{"", "cloud", false, true, DisplayWindow},
		{"", "cloud", true, true, DisplayWindow},

		// The same VMs on a host with no display server. qemu would exit 1
		// on the window rather than fall back, so stoat falls back for them.
		{"", "disk", false, false, DisplayVNC},
		{"auto", "disk", false, false, DisplayVNC},
		{"", "cloud", false, false, DisplayVNC},

		// "window" pins the surface, on every mode/installed combination,
		// as long as the host has a session.
		{"window", "live", false, true, DisplayWindow},
		{"window", "disk", true, true, DisplayWindow},
		{"window", "cloud", false, true, DisplayWindow},
		// "vnc" pins the other way, including the disk-mode case the auto
		// default would have given a window.
		{"vnc", "disk", false, true, DisplayVNC},
		{"vnc", "live", true, true, DisplayVNC},

		// graphical is a veto: it wins over "window" too, so a headless
		// host never gets handed a qemu argv that makes qemu exit 1.
		{"window", "disk", false, false, DisplayVNC},
		{"window", "live", true, false, DisplayVNC},
	} {
		if got := DisplayKind(c.pref, c.graphical); got != c.want {
			t.Errorf("DisplayKind(%q, graphical=%v) = %q, want %q", c.pref, c.graphical, got, c.want)
		}
	}
}

// DisplayKind must answer from its arguments alone. It is called from a JSON
// DTO constructor, so an environment read here would make the wire value
// depend on the machine the CLI happened to run on rather than on the host
// fact its caller resolved.
func TestDisplayKindReadsNoEnvironment(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv(GraphicalEnv, "0")
	if got := DisplayKind("", true); got != DisplayWindow {
		t.Errorf("DisplayKind = %q, want %q: the argument decides, not the environment", got, DisplayWindow)
	}
}

// NeedsWindow is a wrapper over DisplayKind. Every mode and installed state
// now wants a window on a graphical host, since qemu.Args and the TUI both
// branch on it.
func TestNeedsWindowMatchesDisplayKind(t *testing.T) {
	for _, mode := range []string{"live", "disk", "cloud"} {
		for _, installed := range []bool{false, true} {
			v := &config.VM{Mode: mode, Installed: installed}
			if got := NeedsWindow(v, true); !got {
				t.Errorf("NeedsWindow(mode=%q installed=%v, graphical=true) = %v, want true", mode, installed, got)
			}
			// WantsWindow is the same question with the host taken out of it,
			// which is what separates "this VM never gets a window" from "this
			// host could not give it one".
			if got := WantsWindow(v); !got {
				t.Errorf("WantsWindow(mode=%q installed=%v) = %v, want true", mode, installed, got)
			}
			if NeedsWindow(v, false) {
				t.Errorf("no host session, yet NeedsWindow(mode=%q installed=%v) said yes", mode, installed)
			}
		}
	}
}

// The install flow, pinned end to end at the argv: a fresh disk VM on a host
// with a graphical session gets a real GTK window and no VNC socket, because
// setup-alpine is driven at that window or not at all.
//
// The host fact is taken from GraphicalSession over a real session's
// variables, not passed as a literal true, so the pin covers the whole
// chain. Args(v, true) alone would keep passing even if detection started
// answering "no session" on a machine that has one.
func TestFreshDiskVMStillGetsARealWindow(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv(GraphicalEnv, "")
	if !GraphicalSession() {
		t.Fatal("a session with WAYLAND_DISPLAY set must be detected as graphical")
	}
	v := &config.VM{
		Name: "alpinedisk", Mode: "disk", OS: "alpine", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 2, Disk: "8G", Installed: false, SSHPort: 2200,
		Dir: filepath.Join("/data", "alpinedisk"),
	}
	got := joined(Args(v, GraphicalSession()))
	if !strings.Contains(got, "-display gtk,gl=on") {
		t.Errorf("an uninstalled disk VM must open a real qemu window:\n%s", got)
	}
	if !strings.Contains(got, "-vga virtio") {
		t.Errorf("the installer draws to VGA and needs an adapter:\n%s", got)
	}
	if strings.Contains(got, "-display none") || strings.Contains(got, "-vnc") {
		t.Errorf("the install console must not be headless:\n%s", got)
	}

	// ...and after install, the same VM still opens a window: "auto" no
	// longer singles out the installer console.
	v.Installed = true
	got = joined(Args(v, GraphicalSession()))
	if !strings.Contains(got, "-display gtk,gl=on") {
		t.Errorf("an installed disk VM on a graphical host must still open a window:\n%s", got)
	}
}

// The bug: on a host with no graphical session, this VM could not start at
// all, because qemu exits 1 on -display gtk rather than degrading. The
// install console falls back to VNC instead, where a human on another
// machine can drive it.
func TestFreshDiskVMOnAHeadlessHostFallsBackToVNC(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "alpinedisk", Mode: "disk", OS: "alpine", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 2, Disk: "8G", Installed: false, SSHPort: 2200,
		Dir: filepath.Join("/data", "alpinedisk"),
	}
	got := joined(Args(v, false))
	if !strings.Contains(got, "-display none -vnc unix:/data/alpinedisk/vnc.sock") {
		t.Errorf("with no session the install console must bind VNC:\n%s", got)
	}
	if strings.Contains(got, "gtk") {
		t.Errorf("a window on a host with no session is the failure, not the goal:\n%s", got)
	}
	// The boot media is the install ISO either way: only the surface moved.
	if !strings.Contains(got, "-cdrom /data/isos/alpine.iso") || !strings.Contains(got, "-boot d") {
		t.Errorf("the fallback must still boot the installer:\n%s", got)
	}
}

func TestAttachVNCPrefersDirectViewer(t *testing.T) {
	fakeBins(t, "gvncviewer", "socat")
	a := AttachVNC("/data/work/vnc.sock")
	if a.Command != "gvncviewer /data/work/vnc.sock" {
		t.Errorf("Command = %q", a.Command)
	}
	if a.Then != "" {
		t.Errorf("a direct viewer needs no second step, got %q", a.Then)
	}
	if a.Missing != nil {
		t.Errorf("Missing must be empty when a viewer was found: %v", a.Missing)
	}
}

func TestAttachVNCFallsBackToTheSocatBridge(t *testing.T) {
	fakeBins(t, "socat")
	a := AttachVNC("/data/work/vnc.sock")
	if !strings.HasPrefix(a.Command, "socat ") || !strings.Contains(a.Command, "UNIX-CONNECT:/data/work/vnc.sock") {
		t.Errorf("Command = %q", a.Command)
	}
	// The bridge alone shows nobody anything; the follow-up step is the half
	// that makes it usable, so it must never be silently absent.
	if !strings.Contains(a.Then, "127.0.0.1:5900") {
		t.Errorf("Then = %q, want the address a client connects to", a.Then)
	}
	if !strings.Contains(a.Command, "bind=127.0.0.1") {
		t.Errorf("the bridge must not publish the VM's screen to the LAN: %q", a.Command)
	}
}

// A command naming a binary the user does not have reads as an instruction
// and fails as one. Nothing installed must produce no command at all.
func TestAttachVNCWithNothingInstalledNamesWhatToInstall(t *testing.T) {
	fakeBins(t)
	a := AttachVNC("/data/work/vnc.sock")
	if a.Command != "" || a.Then != "" {
		t.Errorf("no viewer installed, yet got Command=%q Then=%q", a.Command, a.Then)
	}
	if len(a.Missing) == 0 {
		t.Error("Missing must name what to install")
	}
	for _, want := range []string{"gvncviewer", "socat"} {
		if !strings.Contains(strings.Join(a.Missing, " "), want) {
			t.Errorf("Missing = %v, want it to mention %q", a.Missing, want)
		}
	}
}

// A data root with a space in it must still yield a command that runs, and
// the ordinary path must stay copy-pasteable without quotes around it.
func TestAttachVNCQuotesOnlyWhenItHasTo(t *testing.T) {
	fakeBins(t, "gvncviewer")
	if a := AttachVNC("/home/u/my vms/w/vnc.sock"); a.Command != "gvncviewer '/home/u/my vms/w/vnc.sock'" {
		t.Errorf("Command = %q", a.Command)
	}
	if a := AttachVNC("/home/u/.stoat/w/vnc.sock"); strings.Contains(a.Command, "'") {
		t.Errorf("an ordinary path must not be quoted: %q", a.Command)
	}
}
