package core

import (
	"github.com/novusedge/stoat/internal/hostcheck"
)

// HostCheck is one host requirement and its fix, returned as data so the TUI,
// CLI and MCP server render it their own way. Named HostCheck, not hostcheck's
// bare Check, so a caller importing both reads them as distinct types.
type HostCheck struct {
	Name   string
	OK     bool
	Detail string   // "/usr/bin", "not found", "permission denied"
	Fix    []string // shell commands, already distro-resolved; empty when OK
	// Optional marks a binary some commands need; a missing one is not a broken host.
	Optional bool
}

// Doctor probes every host dependency and reports it as data, printing
// nothing, so an MCP server or TUI panel can reuse it. The two earlier doctors
// (installer's checklist, the CLI's `stoat doctor`) both wrote to a Writer.
//
// It adapts hostcheck.RunChecks, which probes Linux's qemu-system-x86_64,
// qemu-img, ssh, xorriso and /dev/kvm requirements. Other hosts receive one
// native qualification result instead of Linux dependency claims. ssh-keygen
// gets no separate check: it ships in the same package as ssh on Linux.
func Doctor() []HostCheck {
	checks := hostcheck.RunChecks(hostcheck.DetectDistro())
	out := make([]HostCheck, len(checks))
	for i, c := range checks {
		out[i] = HostCheck{Name: c.Name, OK: c.OK, Detail: c.Detail, Fix: c.Fix, Optional: c.Optional}
	}
	return out
}

// internal/hostcheck holds the probes so core does not import internal/
// installer. The probes once lived in installer/checks.go, distro.go and
// kvm_linux.go, but installer/tui.go is a Bubbletea TUI, so importing it
// pulled charm.land/bubbletea, lipgloss and bubbles into core, the headless
// layer an MCP server sits on. Check with
// `go list -deps ./internal/core | grep charm.land`.
//
// The three files moved to hostcheck verbatim; installer keeps an alias shim
// so tui.go, build.go and cmd/installer compile unedited. installer still
// calls hostcheck.RunChecks directly, which is fine: the installer runs before
// the built stoat binary exists, so both sides sit on the same leaf.
