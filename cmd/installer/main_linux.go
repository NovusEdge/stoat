//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"

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

	// ponytail: --no-tty is the only flag; full flag parsing if more needed
	if len(os.Args) > 1 && os.Args[1] == "--no-tty" {
		return runHeadless(repoDir, home)
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

func runHeadless(repoDir, home string) int {
	fmt.Println("checking host")
	checks := installer.RunChecks(installer.DetectDistro())
	for _, c := range checks {
		status := "ok"
		if !c.OK {
			status = "warn"
		}
		fmt.Printf("  %-6s %s\t%s\n", status, c.Name, c.Detail)
	}

	dir := installer.DefaultDir(home, os.Getenv("PREFIX"))
	version := installer.Version(repoDir)

	fmt.Printf("\nbuilding %s...\n", version)
	tmp, err := os.MkdirTemp("", "stoat-build-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	outPath := filepath.Join(tmp, "stoat")
	if err := installer.Build(repoDir, version, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	fmt.Printf("installing to %s...\n", dir)
	binPath, err := installer.Install(outPath, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	if err := installer.InstallData(repoDir, home); err != nil {
		fmt.Fprintln(os.Stderr, "error setting up ~/.stoat:", err)
		return 1
	}

	fmt.Printf("done: stoat %s at %s\n", version, binPath)

	if !installer.OnPath(dir, os.Getenv("PATH")) {
		fmt.Printf("\nnote: %s is not on your PATH\n", dir)
	}
	return 0
}
