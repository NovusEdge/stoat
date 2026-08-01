//go:build linux

package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/installer"
)

func run() int {
	repoDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine the working directory:", err)
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine your home directory:", err)
		return 1
	}

	m := installer.New(repoDir, home, os.Getenv("SHELL"), os.Getenv("PATH"), os.Getenv("PREFIX"))

	final, err := tea.NewProgram(m).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "installer failed:", err)
		return 1
	}
	if fm, ok := final.(installer.Model); ok && fm.Failed() {
		return 1
	}
	return 0
}
