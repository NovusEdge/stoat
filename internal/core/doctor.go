package core

import (
	"github.com/novusedge/stoat/internal/hostcheck"
)

// HostCheck is one host requirement and what to do when it is missing,
// returned as data rather than printed text so the TUI, the CLI and an MCP
// server can each render it however suits them. Named HostCheck rather than
// the bare "Check" internal/hostcheck uses for the same shape, because a
// caller that imports both (this package does) needs the two to read as
// distinct types, not as one accidentally shadowing the other.
type HostCheck struct {
	Name   string
	OK     bool
	Detail string   // "/usr/bin", "not found", "permission denied"
	Fix    []string // shell commands, already distro-resolved; empty when OK
}

// Doctor probes every host dependency stoat needs and reports the result as
// data. It takes no interactive input and prints nothing — the two doctors
// that existed before this one (installer's pre-install checklist and the
// CLI's `stoat doctor`) both wrote straight to a Writer, which is exactly
// what stops an MCP server or a TUI panel from reusing either.
//
// Doctor is a thin adapter over internal/hostcheck, not a second
// implementation of the same probes. See the package doc below for why that
// package exists rather than this one calling into internal/installer
// directly.
//
// internal/hostcheck.RunChecks already covers the union of what both prior
// doctors checked:
//   - qemu-system-x86_64, qemu-img, ssh, xorriso (LookPath, binChecks) —
//     matches the CLI's qemu.Preflight() binary probe and its own separate
//     `ssh` LookPath.
//   - /dev/kvm (KVMCheck) — matches qemu.Preflight()'s /dev/kvm open.
//
// ssh-keygen is deliberately not a separate check: ssh and ssh-keygen ship in
// one package on every distro stoat supports, so a second probe would only
// ever fail or pass in lockstep with the ssh check already here.
func Doctor() []HostCheck {
	checks := hostcheck.RunChecks(hostcheck.DetectDistro())
	out := make([]HostCheck, len(checks))
	for i, c := range checks {
		out[i] = HostCheck{Name: c.Name, OK: c.OK, Detail: c.Detail, Fix: c.Fix}
	}
	return out
}

// Why this package exists, and why core depends on it rather than on
// internal/installer directly:
//
// The probes (LookPath on qemu-system-x86_64/qemu-img/ssh/xorriso, and
// /dev/kvm) used to live in internal/installer/checks.go, distro.go and
// kvm_linux.go. That looked leaf-ish at a glance — those three files import
// nothing beyond errors/io/fs/os/exec/filepath/strings — but
// internal/installer as a WHOLE is not a leaf: internal/installer/tui.go is
// a full Bubbletea TUI. Importing internal/installer from here pulled
// charm.land/bubbletea, lipgloss, bubbles and log into internal/core's
// dependency graph — verifiable with `go list -deps ./internal/core | grep
// charm.land`. internal/core is the headless layer an MCP server sits on
// (see the package doc in core.go); it must never depend on a terminal UI,
// so that import inverted the exact layering this branch exists to fix.
//
// The fix is this package: the three check files, moved here verbatim
// (package clause aside), with nothing above them. internal/installer keeps
// a small alias shim (see its checks.go) so tui.go, build.go and
// cmd/installer/main_linux.go compile unedited against the old names.
//
// internal/core already depends on internal/config (see core.go), so core
// importing this small, genuinely dependency-free package adds nothing
// architecturally new; internal/hostcheck importing core would, and would
// also be backwards — a leaf importing the layer above it.
//
// The follow-up this leaves: internal/installer's tui.go/build.go still call
// hostcheck.RunChecks through the alias shim rather than through
// core.Doctor. That is fine, not a defect — the installer builds and
// installs stoat itself, so it running before core.Doctor's caller (the
// built stoat binary) even exists is expected, not a layering violation to
// fix later. Both sides now sit on the same leaf; neither needs to depend on
// the other.
