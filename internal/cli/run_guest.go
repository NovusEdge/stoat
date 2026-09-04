package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runGuest implements "guest ls" and "guest show".
func runGuest(a *Args, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "ls":
		gs := core.Guests()
		if a.JSON {
			return a.ok(stdout, wire.GuestList{Guests: wire.FromGuests(gs)})
		}
		fmt.Fprintf(stdout, "%-10s %-8s %-8s %-10s %s\n", "NAME", "INIT", "PKG", "BACKEND", "SOURCE")
		for _, g := range gs {
			pkg := ""
			if len(g.Pkg.Install) > 0 {
				pkg = g.Pkg.Install[0]
			}
			fmt.Fprintf(stdout, "%-10s %-8s %-8s %-10s %s\n", g.Name, g.Init, pkg, g.DefaultBackend, g.Source)
		}
		return ExitOK

	case "show":
		g, err := core.Guest(a.VM)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			return a.ok(stdout, wire.GuestShow{Guest: wire.FromGuest(g)})
		}
		w := wire.FromGuest(g)
		fmt.Fprintf(stdout, "name:             %s (%s)\n", w.Name, w.Source)
		fmt.Fprintf(stdout, "init:             %s\n", w.Init)
		fmt.Fprintf(stdout, "shell:            %s\n", w.Shell)
		fmt.Fprintf(stdout, "default backend:  %s\n", w.DefaultBackend)
		fmt.Fprintf(stdout, "default ssh user: %s\n", w.DefaultSSHUser)
		fmt.Fprintf(stdout, "escalate:         %v\n", w.Escalate)
		fmt.Fprintf(stdout, "capabilities:     %v\n", w.Capabilities)
		fmt.Fprintf(stdout, "aliases:          %v\n", w.Aliases)
		fmt.Fprintf(stdout, "pkg setup:        %s\n", w.Pkg.Setup)
		fmt.Fprintf(stdout, "pkg install:      %v\n", w.Pkg.Install)
		for _, k := range []string{"enable", "start", "stop", "restart", "status"} {
			fmt.Fprintf(stdout, "svc %-8s      %s\n", k+":", w.Svc[k])
		}
		return ExitOK
	}
	// Unreachable: Parse rejects any action but ls/show.
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError("guest: unknown action "+a.Sub))
		return ExitUsage
	}
	fmt.Fprintln(stderr, "stoat: guest: unknown action", a.Sub)
	return ExitUsage
}
