package core

import (
	"context"
	"errors"

	"github.com/novusedge/stoat/internal/qemu"
)

// AutoRestartAfterInstall waits for an uninstalled disk VM's unattended
// installer to power off, then restarts the VM so it boots the disk the
// installer just wrote.
//
// The precondition disarms itself after one restart: qemu.Start flips
// v.Installed the first time it sees real bytes on the disk (run.go's
// diskWritten check), so a second call against the same VM reads Installed
// and returns immediately with no wait.
//
// apkovlBackend.Args adds -no-reboot for this exact boot, so a successful
// install's own "poweroff" (internal/apkovl's installScript) exits QEMU
// instead of re-entering the installer. A failed install leaves the
// installer's shell running, so qemu.Running never turns false and this
// call rides out ctx's deadline instead of restarting.
func AutoRestartAfterInstall(ctx context.Context, name string) (bool, error) {
	v, err := load(name)
	if err != nil {
		return false, err
	}
	if v.Mode != "disk" || v.Installed || !qemu.Running(v) {
		return false, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()
	if err := waitStopped(waitCtx, v); err != nil {
		// ctx cancellation or InstallTimeout: give up silently, matching
		// awaitSSH's contract (internal/tui/autoprov.go) for a watch nobody
		// explicitly asked to be told about.
		return false, nil
	}

	if err := Start(name); err != nil && !errors.Is(err, ErrAlreadyRunning) {
		return false, err
	}
	return true, nil
}
