package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/mcpsrv"
)

// runMCP dispatches the mcp subcommands. serve blocks until the client
// disconnects or the context ends, so it emits no result line: the JSON
// contract is the tool traffic itself, not this command's own envelope.
func runMCP(a *Args, version string, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "serve":
		opts := mcpsrv.Options{Version: version, Limits: a.Limits}
		var err error
		if a.HTTP != "" {
			err = mcpsrv.ServeHTTP(context.Background(), a.HTTP, opts)
		} else {
			err = mcpsrv.ServeStdio(context.Background(), opts)
		}
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		return ExitOK
	case "install":
		report, err := mcpsrv.Install(a.Client, mcpsrv.InstallOpts{Project: a.InstallProject, Print: a.Print})
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.Print {
			fmt.Fprintln(stdout, report.JSON)
			return ExitOK
		}
		if a.JSON {
			return a.ok(stdout, report)
		}
		fmt.Fprintf(stdout, "wrote %s\n", report.Path)
		return ExitOK
	case "doctor":
		r := mcpsrv.DoctorReport(version)
		if a.JSON {
			return a.ok(stdout, r)
		}
		fmt.Fprintf(stdout, "contract %d, transport %s\n", r.Contract, r.Transport)
		fmt.Fprintf(stdout, "binary: %s\n", r.Binary)
		for _, c := range r.Clients {
			status := "not installed"
			if c.Installed {
				status = "installed"
				if !c.Current {
					status = "installed (stale)"
				}
			}
			fmt.Fprintf(stdout, "  %-14s %s\n", c.Client, status)
		}
		return ExitOK
	}
	// Unreachable: Parse rejects any Sub but serve/install/doctor.
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError("mcp: unknown subcommand "+a.Sub))
		return ExitUsage
	}
	fmt.Fprintln(stderr, "stoat: mcp: unknown subcommand", a.Sub)
	return ExitUsage
}
