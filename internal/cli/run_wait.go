package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/novusedge/stoat/internal/core"
)

// runWait blocks until a.VM reaches a.Until, or a.Timeout expires. core.Wait
// already refuses the impossible cases (ErrCannotReach) before ever touching
// ctx, so a stopped VM asked to become reachable fails fast rather than
// waiting out the whole timeout.
func runWait(a *Args, stdout, stderr io.Writer) int {
	if a.VM == "" {
		return fanOut(a, stdout, stderr, func(name string) error {
			ctx, cancel := context.WithTimeout(context.Background(), a.Timeout)
			defer cancel()
			return core.Wait(ctx, name, a.Until)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.Timeout)
	defer cancel()

	start := time.Now()
	stopProgress := func() {}
	if !a.JSON && !a.Quiet && terminal(stderr) {
		stopProgress = waitProgress(stderr, a.VM, a.Until, start)
	}
	err := core.Wait(ctx, a.VM, a.Until)
	stopProgress()
	waited := time.Since(start)

	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm":        a.VM,
			"until":     string(a.Until),
			"reached":   true,
			"waited_ms": waited.Milliseconds(),
		})
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "%s reached %s (%dms)\n", a.VM, a.Until, waited.Milliseconds())
	}
	return ExitOK
}

// waitProgress rewrites one stderr line every second so a wait that takes
// most of a minute does not look hung. The returned func stops it and clears
// the line before any result prints.
func waitProgress(w io.Writer, vm string, until core.Until, start time.Time) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			fmt.Fprintf(w, "\rwaiting for %s to be %s... %ds", vm, until, int(time.Since(start).Seconds()))
			select {
			case <-done:
				fmt.Fprint(w, "\r\033[K")
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done); <-stopped }
}
