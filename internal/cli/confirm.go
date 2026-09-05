package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// confirm is the one gate every destructive command uses. It only reads a
// response from a terminal; pipes and JSON callers must opt in with -y.
func confirm(a *Args, stdin io.Reader, stdout, stderr io.Writer, prompt string) (bool, int) {
	if a.Yes {
		return true, ExitOK
	}
	if a.JSON {
		return false, a.failMsg(stdout, stderr, wire.ErrConfirmationRequired, prompt+"; pass -y to confirm")
	}
	if a.Quiet || !terminal(stdin) || !terminal(stdout) {
		return false, a.failMsg(stdout, stderr, wire.ErrConfirmationRequired, prompt+"; pass -y to confirm")
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
	return term.IsTerminal(f.Fd())
}
