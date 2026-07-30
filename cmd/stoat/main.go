package main

import (
	"fmt"
	"os"

	"github.com/novusedge/stoat/internal/cli"
	"github.com/novusedge/stoat/internal/tui"
)

var version = "dev"

func main() {
	// Bare `stoat`, with no subcommand, launches the TUI exactly as before.
	if len(os.Args) <= 1 {
		if err := tui.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "stoat:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] == "-v" || os.Args[1] == "--version" {
		fmt.Println("stoat", version)
		return
	}
	os.Exit(cli.Main(os.Args[1:], version, os.Stdin, os.Stdout, os.Stderr))
}
