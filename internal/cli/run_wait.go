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
	ctx, cancel := context.WithTimeout(context.Background(), a.Timeout)
	defer cancel()

	start := time.Now()
	err := core.Wait(ctx, a.VM, a.Until)
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
		_, _ = fmt.Fprintf(stdout, "%s reached %s (%dms)\n", a.VM, a.Until, waited.Milliseconds())
	}
	return ExitOK
}
