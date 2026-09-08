package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/project"
)

// initTemplate is the annotated sample stoat init writes. Every key in it
// exists on project's own structs, so the file decodes in Reject mode;
// TestInitOutputLoads is what holds that true.
func initTemplate(name string) string {
	return fmt.Sprintf(`# VMs for this repo. Commit this file. Then run: stoat up
schema = 1

[project]
# VM name prefix. "dev" below becomes "%s-dev".
name = %q

# Recipes to download. Then run: stoat recipe lock
[recipes]

[vms.dev]
# Required. An id from "stoat images", or a path to your own image.
image = "ubuntu-24.04"
# Delete a line below to get the default.
cpus = 4
ram = 4096
disk = "20G"
# Recipe names. Applied on every "stoat up".
recipes = []
# Dirs to share with the VM, under /work. "." is this repo.
shares = ["."]
# What an agent can do: none, observe, manage, exec.
agent_access = "manage"

# Recipe settings. Put secrets in .stoat/secrets.toml, and do not commit it.
# [vms.dev.params.docker]
# user = "dev"
`, name, name)
}

// runInit writes stoat.toml and gitignores the cache directory.
//
// It refuses an existing file rather than merging into it: the file is the
// user's own declaration, and there is no safe automatic edit of one.
func runInit(a *Args, stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	path := filepath.Join(dir, project.FileName)
	if _, err := os.Stat(path); err == nil {
		return a.failMsg(stdout, stderr, os.ErrExist, project.FileName+" already exists")
	}

	name := a.Tag
	if name == "" {
		name = strings.ToLower(filepath.Base(dir))
	}
	if err := os.WriteFile(path, []byte(initTemplate(name)), 0o644); err != nil {
		return a.fail(stdout, stderr, err)
	}

	ignored, err := ignoreCacheDir(dir)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}

	if a.JSON {
		return a.ok(stdout, wire.InitResult{Path: path, Project: name, GitignoreUpdated: ignored})
	}
	if !a.Quiet {
		a.prose(stdout).Step("%s created in %s", project.FileName, dir)
		if ignored {
			a.prose(stdout).Step("added %s/ to .gitignore", project.CacheDir)
		}
	}
	return ExitOK
}

// ignoreCacheDir appends ".stoat/" to .gitignore, in a git checkout only and
// only when the line is absent. Outside a checkout there is nothing to
// ignore, so it does nothing and says so.
func ignoreCacheDir(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false, nil
	}
	path := filepath.Join(dir, ".gitignore")
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	line := project.CacheDir + "/"
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		body = append(body, '\n')
	}
	body = append(body, []byte(line+"\n")...)
	return true, os.WriteFile(path, body, 0o644)
}
