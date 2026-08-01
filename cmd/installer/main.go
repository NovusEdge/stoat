// Command installer builds stoat from this clone and installs it.
//
// Run it with `just setup`. It is deliberately not a released artifact: it
// builds from source, so it only makes sense from a checkout.
package main

import (
	"fmt"
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/installer"
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "stoat is Linux-only: it needs KVM, and it does not compile for "+runtime.GOOS+" yet.")
		os.Exit(1)
	}

	repoDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine the working directory:", err)
		os.Exit(1)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine your home directory:", err)
		os.Exit(1)
	}

	m := installer.New(repoDir, home, os.Getenv("SHELL"), os.Getenv("PATH"), os.Getenv("PREFIX"))

	final, err := tea.NewProgram(m).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "installer failed:", err)
		os.Exit(1)
	}
	if fm, ok := final.(installer.Model); ok && fm.Failed() {
		os.Exit(1)
	}
}
