package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/mcpsrv"
)

func daemonDTO(d mcpsrv.Daemon) wire.MCPDaemon {
	return wire.MCPDaemon{Dir: d.Dir, Addr: d.Addr, PID: d.PID, Started: d.Started, Log: d.Log}
}

// runMCP dispatches the mcp subcommands. serve blocks until the client
// disconnects or the context ends, so it emits no result line: the JSON
// contract is the tool traffic itself, not this command's own envelope.
func runMCP(a *Args, version string, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "serve":
		opts := mcpsrv.Options{Version: version, Limits: a.Limits}
		var err error
		if a.HTTP != "" {
			if !a.Quiet && !a.JSON {
				opts.Notify = stderr
				// An HTTP server is a foreground process with a terminal, so
				// mirror the log there. A stdio server cannot: the client owns
				// both its streams.
				logx.Tee(stderr)
			}
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
	case "up":
		dir, err := os.Getwd()
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		d, err := mcpsrv.Up(dir, a.HTTP)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			return a.ok(stdout, daemonDTO(d))
		}
		fmt.Fprintf(stdout, "serving %s on %s (pid %d)\n", d.Dir, d.Addr, d.PID)
		return ExitOK
	case "down":
		dir, err := os.Getwd()
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		d, err := mcpsrv.Down(dir)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			return a.ok(stdout, daemonDTO(d))
		}
		fmt.Fprintf(stdout, "stopped %s on %s\n", d.Dir, d.Addr)
		return ExitOK
	case "status":
		ds, err := mcpsrv.Status()
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		list := wire.MCPDaemonList{Servers: make([]wire.MCPDaemon, 0, len(ds))}
		for _, d := range ds {
			list.Servers = append(list.Servers, daemonDTO(d))
		}
		if a.JSON {
			return a.ok(stdout, list)
		}
		if len(list.Servers) == 0 {
			fmt.Fprintln(stdout, "no mcp server is running")
			return ExitOK
		}
		for _, s := range list.Servers {
			fmt.Fprintf(stdout, "%-22s %-10d %s\n", s.Addr, s.PID, s.Dir)
		}
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
	// Unreachable: Parse rejects any Sub but serve/install/doctor/up/down/status.
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError("mcp: unknown subcommand "+a.Sub))
		return ExitUsage
	}
	fmt.Fprintln(stderr, "stoat: mcp: unknown subcommand", a.Sub)
	return ExitUsage
}
