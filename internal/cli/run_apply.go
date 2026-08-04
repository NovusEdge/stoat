package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runApply runs a VM's recipes and streams their output live, the same
// pattern as runProvision (run_access.go): the work happens in a goroutine
// while the apply log file is tailed and copied out as it grows.
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
	// appended line becomes a "log" event instead, same as runProvision.
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
