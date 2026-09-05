package sshx

import (
	"context"
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/config"
)

// Run is not implemented yet. Task 9 executes argv inside v's guest over
// ssh, quoting it for the guest shell, and returns the guest's raw output
// and exit status. A command that ran and exited non-zero is a result, not
// an error; Run returns an error only when ssh could not run at all.
func Run(ctx context.Context, v *config.VM, root bool, argv []string, stdin io.Reader) ([]byte, []byte, int, error) {
	return nil, nil, 0, fmt.Errorf("sshx.Run: not implemented")
}
