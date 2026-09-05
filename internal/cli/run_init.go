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
	return fmt.Sprintf(`# stoat.toml: this repository's VMs. Commit it.
# Run stoat up to build and start every VM declared here.
schema = 1

[project]
# The prefix for a VM's global name. "dev" below becomes "%s-dev".
name = %q

# Remote recipes, by index name or by source. stoat recipe lock pins each one
# to a commit in stoat.lock, which you also commit.
[recipes]

[vms.dev]
# image is the only required field: a catalog id from stoat images, or a path
# to your own image, relative to this file.
image = "ubuntu-24"
# Every field below takes stoat new's default when you delete it.
cpus = 4
ram = 4096
disk = "20G"
# Recipe names, applied in dependency order on every stoat up.
recipes = []
# Directories from this project, mounted under /work in the guest. "." is the
# project root and mounts at /work. Every entry must stay inside the project.
shares = ["."]
# What an MCP agent may do: none, observe, manage, exec.
agent_access = "manage"

# Non-secret recipe params. Secrets go in .stoat/secrets.toml, which stoat
# writes 0600 and never commits.
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
		fmt.Fprintf(stdout, "wrote %s\n", project.FileName)
		if ignored {
			fmt.Fprintf(stdout, "added %s/ to .gitignore\n", project.CacheDir)
		}
		fmt.Fprintln(stdout, "edit it, then run: stoat up")
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
