package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

func runScreenshot(a *Args, stdout, stderr io.Writer) int {
	shot, err := core.Screenshot(a.VM, a.Out)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, wire.FromShot(a.VM, shot))
	}
	fmt.Fprintf(stdout, "%s (%dx%d, %d bytes)\n", shot.Path, shot.Width, shot.Height, shot.Bytes)
	return ExitOK
}
