package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/hostcheck"
)

// toHostChecks adapts core.Doctor's result to capabilities.Input.HostChecks.
// The two types have identical fields but capabilities cannot import core:
// core now imports provider, which imports capabilities, so the reverse
// import would close a cycle.
func toHostChecks(cs []core.HostCheck) []hostcheck.Check {
	out := make([]hostcheck.Check, len(cs))
	for i, c := range cs {
		out[i] = hostcheck.Check{Name: c.Name, OK: c.OK, Detail: c.Detail, Fix: c.Fix, Optional: c.Optional}
	}
	return out
}

func runCapabilities(a *Args, version string, stdout, stderr io.Writer) int {
	var target *capabilities.Target
	if a.VM != "" {
		loaded, err := capabilities.LoadTarget(a.VM)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		target = &loaded
	}
	projectState := "absent"
	if a.Project != nil {
		projectState = "available"
	}
	report := capabilities.Build(capabilities.Input{
		Version:      version,
		ProjectState: projectState,
		HostChecks:   toHostChecks(core.Doctor()),
		Target:       target,
	})
	if a.JSON {
		return a.ok(stdout, wire.Capabilities(report))
	}
	fmt.Fprintln(stdout, "NAME                         STATUS      SCOPE")
	for _, profile := range report.Profiles {
		fmt.Fprintf(stdout, "%-28s %-11s %s\n", profile.Name, profile.Status, profile.Scope)
	}
	for _, entry := range report.Capabilities {
		fmt.Fprintf(stdout, "%-28s %-11s %s\n", entry.Name, entry.Status, entry.Scope)
	}
	for _, entry := range report.Unavailable {
		fmt.Fprintf(stdout, "%-28s %-11s %s\n", entry.Name, entry.Status, entry.Scope)
	}
	return ExitOK
}
