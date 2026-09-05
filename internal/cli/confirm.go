package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// confirm is the one gate every destructive command uses. It only reads a
// response from a terminal; pipes and JSON callers must opt in with -y.
func confirm(a *Args, stdin io.Reader, stdout io.Writer, prompt string) (bool, int) {
	if a.Yes {
		return true, ExitOK
	}
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.MapError(fmt.Errorf("%w: %s", wire.ErrConfirmationRequired, prompt)))
		return false, ExitFail
	}
	if a.Quiet || !terminal(stdin) || !terminal(stdout) {
		return false, ExitFail
	}
	fmt.Fprintf(stdout, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Fprintln(stdout, "aborted")
		return false, ExitFail
	}
	return true, ExitOK
}

func terminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok || f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
