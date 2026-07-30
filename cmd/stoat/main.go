package main

import (
	"fmt"
	"os"

	"github.com/novusedge/stoat/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println("stoat", version)
		return
	}
	if err := tui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "stoat:", err)
		os.Exit(1)
	}
}
