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
	if a.VM == "" {
		return fanOut(a, stdout, stderr, func(name string) error {
			sub := *a
			sub.VM, sub.JSON = name, false
			if code := runApply(&sub, stdout, stderr); code != ExitOK {
				return fmt.Errorf("%s: apply failed", name)
			}
			return nil
		})
	}
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}

	if a.DryRun {
		return runApplyDryRun(a, stdout, stderr)
	}

	applied := a.Only
	if len(applied) == 0 {
		applied = v.Recipes
	}

	if !a.Quiet {
		fmt.Fprintf(stdout, "applying recipes to %s...\n", a.VM)
	}

	// Under --json, raw log bytes must not reach stdout: they would sit
	// inside the JSON Lines stream and break every consumer's parse. Each
	// appended line becomes a "log" event instead.
	out := stdout
	var lw *jsonLogWriter
	if a.JSON {
		lw = &jsonLogWriter{em: wire.NewEmitter(stdout), cmd: a.Cmd}
		out = lw
	}
	redactor, err := newSecretRedactor(v.Paths.Dir, out)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}

	done := make(chan error, 1)
	go func() { done <- core.Apply(context.Background(), a.VM, core.ApplyOpts{Only: a.Only}) }()
	aerr := streamFile(v.Paths.ApplyLog, redactor, done)
	if redactorErr := redactor.Flush(); aerr == nil && redactorErr != nil {
		aerr = redactorErr
	}
	if lw != nil {
		lw.Flush()
	}
	if errors.Is(aerr, core.ErrProvisionInProgress) {
		// Another run already holds the VM's provision lock. That run's
		// caller owns the error; this one exits clean rather than reporting
		// somebody else's concurrent apply as its own failure.
		if a.JSON {
			// applied stays a list here. It named the recipes on the success
			// path and false on this one, so a consumer iterating it hit a
			// bool. skipped_reason is what distinguishes the two.
			return a.ok(stdout, map[string]any{"vm": a.VM, "applied": []string{}, "skipped_reason": "an apply is already running"})
		}
		fmt.Fprintf(stdout, "%s: an apply is already running\n", a.VM)
		return ExitOK
	}
	if aerr != nil {
		return a.fail(stdout, stderr, aerr)
	}

	if a.JSON {
		if applied == nil {
			applied = []string{}
		}
		return a.ok(stdout, map[string]any{"vm": a.VM, "applied": applied, "skipped_reason": ""})
	}
	fmt.Fprintf(stdout, "%s: recipes applied\n", a.VM)
	return ExitOK
}

// runApplyDryRun prints core.PlanApply's plan and runs nothing. Human output
// is one line per recipe ("xfce (run, never applied)"). The plan is computed
// host-side, so the VM need not be running.
//
// The JSON plan is wrapped in an object. json.md §2 says every result's
// `data` is an object, and a bare array broke that.
func runApplyDryRun(a *Args, stdout, stderr io.Writer) int {
	plan, err := core.PlanApply(a.VM, core.ApplyOpts{Only: a.Only})
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm":      a.VM,
			"dry_run": true,
			"plan":    wire.FromApplyPlans(plan),
		})
	}
	for _, p := range plan {
		reason := p.Reason
		if p.Version != "" {
			reason = fmt.Sprintf("%s at %s", p.Reason, p.Version)
		}
		fmt.Fprintf(stdout, "%s (%s, %s)\n", p.Name, p.Action, reason)
	}
	return ExitOK
}
