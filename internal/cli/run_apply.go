package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runApply runs a VM's recipes and streams their output live: the work
// happens in a goroutine while the apply log file is tailed and copied out
// as it grows. It also serves the "provision" alias (cli.go's dispatch).
//
// core.Get, not config.Load: the load is only here for the log path to tail
// and the VM's recipe list, but config.Load returns an untyped error, so a
// missing VM came back as code "internal" instead of "not_found". core.Apply
// loads and validates independently, so this read cannot desync from it.
func runApply(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}

	applied := a.Only
	if len(applied) == 0 {
		applied = v.Recipes
	}

	if !a.Quiet {
		fmt.Fprintf(stdout, "applying recipes to %s...\n", a.VM)
	}

	done := make(chan error, 1)
	go func() { done <- core.Apply(context.Background(), a.VM, core.ApplyOpts{Only: a.Only}) }()

	// Under --json, raw log bytes must not reach stdout: they would sit
	// inside the JSON Lines stream and break every consumer's parse. Each
	// appended line becomes a "log" event instead.
	out := stdout
	var lw *jsonLogWriter
	if a.JSON {
		lw = &jsonLogWriter{em: wire.NewEmitter(stdout), cmd: a.Cmd}
		out = lw
	}
	aerr := streamFile(v.Paths.ApplyLog, out, done)
	if lw != nil {
		lw.Flush()
	}
	if errors.Is(aerr, core.ErrProvisionInProgress) {
		// Another run already holds the VM's provision lock. That run's
		// caller owns the error; this one exits clean rather than reporting
		// somebody else's concurrent apply as its own failure.
		if a.JSON {
			return a.ok(stdout, map[string]any{"vm": a.VM, "applied": false, "skipped_reason": "an apply is already running"})
		}
		fmt.Fprintf(stdout, "%s: an apply is already running\n", a.VM)
		return ExitOK
	}
	if aerr != nil {
		// core.ErrAppliedAtBoot is a real outcome for a cloud VM, mapped to
		// applied_at_boot by wire's error table; it is not special-cased into
		// a success here.
		return a.fail(stdout, stderr, aerr)
	}

	if a.JSON {
		return a.ok(stdout, map[string]any{"vm": a.VM, "applied": applied})
	}
	fmt.Fprintf(stdout, "%s: recipes applied\n", a.VM)
	return ExitOK
}
