package installer

import "github.com/novusedge/stoat/internal/hostcheck"

// Check, RunChecks, DetectDistro and Problems used to live here directly.
// They now live in internal/hostcheck, a package with no dependency on the
// rest of stoat, so that internal/core (the headless layer an MCP server
// sits on) can reuse the same host-dependency probes without pulling this
// package's Bubbletea TUI (tui.go) into a process that must stay headless —
// see internal/core/doctor.go's doc comment for the full reasoning.
//
// This file exists only so tui.go, build.go and cmd/installer/main_linux.go
// keep compiling unedited: they refer to Check, RunChecks, DetectDistro and
// Problems by these bare names, and a type alias plus func vars make that a
// no-op rename from their point of view.
type Check = hostcheck.Check

var (
	RunChecks    = hostcheck.RunChecks
	DetectDistro = hostcheck.DetectDistro
	Problems     = hostcheck.Problems
)
