package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SSHCall is one invocation of the fake ssh: the argv it was given and the
// remote command, which is ssh's last argument.
type SSHCall struct {
	Argv   []string
	Remote string
}

// SSHCalls reads back the fake ssh's call log on demand, so a test can
// inspect the argv that reached ssh between two Run calls, not only at the
// end.
type SSHCalls struct{ path string }

func (c *SSHCalls) Calls() []SSHCall {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	var out []SSHCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			out = append(out, SSHCall{Argv: strings.Fields(line), Remote: line})
		}
	}
	return out
}

// FakeSSH puts an ssh on PATH that appends its argv to a log and then runs
// script with the remote command's words as "$@". It exists so an in-VM
// tool is testable without a VM: what a test asserts is the argv that
// reached ssh, which is the boundary the tools own.
func FakeSSH(t *testing.T, script string) *SSHCalls {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		// "${@: -1}" is bash-only; dash (Ubuntu's /bin/sh) rejects it with
		// "Bad substitution". This loop is the POSIX way to read the last
		// positional argument.
		"for remote do :; done\n" +
		"eval \"set -- $remote\"\n" +
		script + "\n"
	binPath := filepath.Join(dir, "ssh")
	if err := os.WriteFile(binPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &SSHCalls{path: logPath}
}
