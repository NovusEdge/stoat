package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

// SSHCommand returns the full argv a human would type to ssh into VM name,
// including "ssh" itself as element 0, since the TUI's "drop me into a shell" and
// any caller that just wants to print or hand it to exec.Command need argv[0]
// present, not implied.
//
// The argv construction and the account it resolves to already live in
// sshx.Args/sshx.User: SSHPort, host key checks, and which user to log in as
// (SSHUser when the build recorded one, "root" otherwise; see sshx.User's
// doc comment) are decided there once, so nothing about ssh options is
// duplicated here.
//
// load (vm.go) is used rather than config.Load directly so a broken vm.toml
// comes back as ErrBroken instead of ErrNotFound. That distinction matters
// here specifically: a broken VM has no SSHPort and no SSHUser to build an
// argv from: there is nothing this function could return, so ErrBroken is
// the honest answer, unlike Logs below where the log files don't need
// vm.toml to have parsed at all.
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

// Logs opens the requested log file for VM name.
//
// A missing file is a normal state, not an error: a VM that has never been
// started has no console.log, and one that has never had recipes applied has
// no last-provision.log. Returning an error here would force every caller,
// the TUI's tail view and an agent's "show me the logs" alike, to special-
// case ErrNotExist before they can do anything useful. Instead a missing
// file yields an already-EOF empty reader, so "tail this" and "show me the
// logs" both just see zero bytes, exactly as they would for a file that
// exists but is empty.
//
// A broken vm.toml (load returns ErrBroken) does NOT stop Logs from serving
// the console log. Unlike SSHCommand, nothing here needs a parsed field:
// ConsoleLogPath/ProvisionLogPath are pure filepath.Join(v.Dir, ...), and the
// VM directory is real regardless of whether vm.toml parses. This is also
// the case that matters most in practice: a VM stuck in StateBroken is
// exactly the VM a user wants console output from, to see what went wrong,
// and refusing that here would make the one diagnostic that still works
// unreachable for precisely the VM that needs it. So a directory-only VM is
// built instead, the same pattern vm.go's Destroy already uses for a broken
// VM's directory.
func Logs(name string, which Which) (io.ReadCloser, error) {
	v, err := load(name)
	switch {
	case errors.Is(err, ErrBroken):
		v = &config.VM{Name: name, Dir: filepath.Join(config.Root(), name)}
	case err != nil:
		return nil, err
	}

	var path string
	switch which {
	case WhichConsole:
		path = v.ConsoleLogPath()
	case WhichApply:
		path = v.ProvisionLogPath()
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownWhich, which)
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}
