package main

import (
	"flag"
	"fmt"
	"io"
)

// exitUsage is the exit code for a bad command line, which separates it from
// an installation that started and failed.
const exitUsage = 2

// parseFlags reads the installer's command line. It returns whether to run
// without the terminal UI. An unknown flag, a misplaced flag, or a positional
// argument is an error: the previous parser read os.Args[1] only, so
// `--no-tty` in any other position silently started the interactive UI.
func parseFlags(args []string, out io.Writer) (noTTY bool, err error) {
	flags := flag.NewFlagSet("installer", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintln(out, "usage: go run ./cmd/installer [--no-tty]")
		fmt.Fprintln(out, "\nBuilds stoat from this clone and installs it to $PREFIX,")
		fmt.Fprintln(out, "or to ~/.local/bin when PREFIX is unset.")
		flags.PrintDefaults()
	}
	headless := flags.Bool("no-tty", false, "install without the terminal UI, for CI and scripts")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(out, "unexpected argument:", flags.Arg(0))
		flags.Usage()
		return false, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	return *headless, nil
}
