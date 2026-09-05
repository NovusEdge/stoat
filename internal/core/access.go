package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

// SSHCommand returns the full argv to ssh into VM name, with "ssh" as element
// 0. A caller printing the command or passing it to exec.Command needs argv[0]
// present.
//
// sshx.Args and sshx.User build the argv and resolve the login account
// (SSHPort, host key checks, SSHUser or "root"). SSHCommand duplicates none of
// it.
//
// It calls load, not config.Load, so a broken vm.toml returns ErrBroken. A
// broken VM has no SSHPort or SSHUser to build an argv from.
func SSHCommand(name string) ([]string, error) {
	v, err := load(name)
	if err != nil {
		return nil, err
	}
	return append([]string{"ssh"}, sshx.Args(v)...), nil
}

// Which selects one of a VM's two log files.
type Which string

const (
	// WhichConsole is the VM's serial/graphical console transcript,
	// v.ConsoleLogPath(), written by whatever backend drives the qemu
	// process for the life of the VM.
	WhichConsole Which = "console"
	// WhichApply is the most recent recipe run, v.ProvisionLogPath(),
	// written by sshx.Provision (internal/sshx/sshx.go).
	WhichApply Which = "apply"
)

// ErrUnknownWhich is returned for a Which value that is neither WhichConsole
// nor WhichApply, so a typo'd caller gets a clear error instead of silently
// reading the wrong file.
var ErrUnknownWhich = fmt.Errorf("unknown log selector")

// Whichs returns every log a caller can ask for.
func Whichs() []Which { return []Which{WhichConsole, WhichApply} }

// Valid reports whether w is one of Whichs().
func (w Which) Valid() bool { return slices.Contains(Whichs(), w) }

// Logs opens the requested log file for VM name.
//
// A missing file is normal, not an error. A VM that never started has no
// console.log; one that never ran recipes has no last-provision.log. Logs
// returns an already-EOF empty reader, so a caller sees zero bytes rather than
// special-casing ErrNotExist.
//
// A broken vm.toml does not stop Logs serving the console log. ConsoleLogPath
// and ProvisionLogPath are pure filepath.Join(v.Dir, ...) and need no parsed
// field, and a broken VM is exactly the one a user wants console output from,
// so Logs builds a directory-only VM, like Destroy does.
func Logs(name string, which Which) (io.ReadCloser, error) {
	if !which.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownWhich, which)
	}
	v, err := load(name)
	switch {
	case errors.Is(err, ErrBroken):
		v = &config.VM{Name: name, Dir: filepath.Join(config.Root(), name)}
	case err != nil:
		return nil, err
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	path := v.ProvisionLogPath()
	if which == WhichConsole {
		path = v.ConsoleLogPath()
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return io.NopCloser(bytes.NewReader([]byte(redactLog(string(b), secrets)))), nil
}

func redactLog(value string, secrets config.Secrets) string {
	var values []string
	for _, recipe := range secrets {
		for _, secret := range recipe {
			if secret != "" {
				values = append(values, secret)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, secret := range values {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
}
