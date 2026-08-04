package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// runCopy moves one file between host and guest. Direction was already
// decided by Parse, from which side carried the "<vm>:" prefix.
func runCopy(a *Args, stdout, stderr io.Writer) int {
	var err error
	if a.ToRemote {
		err = core.CopyTo(context.Background(), a.VM, a.Local, a.Remote)
	} else {
		err = core.CopyFrom(context.Background(), a.VM, a.Remote, a.Local)
	}
	if err != nil {
		fmt.Fprintln(stderr, "stoat: cp:", err)
		return ExitFail
	}
	if !a.Quiet {
		if a.ToRemote {
			fmt.Fprintf(stdout, "copied %s to %s:%s\n", a.Local, a.VM, a.Remote)
		} else {
			fmt.Fprintf(stdout, "copied %s:%s to %s\n", a.VM, a.Remote, a.Local)
		}
	}
	return ExitOK
}

// runExec runs a command in a guest and RETURNS THE GUEST'S EXIT CODE, the
// same way ssh itself does. That is what makes it scriptable: `stoat exec vm
// make test && deploy` has to mean what it looks like.
//
// The cost is that stoat's own exit codes and the guest's share one range: a
// guest command exiting 2 is indistinguishable from a stoat usage error. That
// is accepted rather than worked around, because every alternative is worse:
// remapping the guest's status silently lies about what the command did, and a
// dedicated flag for "really give me the real code" would just be a footgun
// with extra steps. ssh made the same trade for the same reason.
//
// Note stoat's OWN failures here (no such VM, not running) still exit 1, and
// print to stderr, so they are distinguishable from guest output.
func runExec(a *Args, stdout, stderr io.Writer) int {
	// No timeout: a guest command may legitimately run for hours, and ^C
	// already kills the ssh process. Bounding it here would mean inventing a
	// limit nobody asked for.
	res, err := core.Exec(context.Background(), a.VM, a.Command)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: exec:", err)
		return ExitFail
	}
	// Streamed through verbatim, on the matching stream, so a caller can pipe
	// stdout without stderr contaminating it.
	fmt.Fprint(stdout, res.Stdout)
	fmt.Fprint(stderr, res.Stderr)
	return res.ExitCode
}

// runSSH replaces this process with ssh via syscall.Exec, so signals and the
// terminal behave exactly as a direct `ssh` invocation would, and stoat
// leaves no supervisor process behind.
func runSSH(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	path, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	argv := append([]string{"ssh"}, sshx.Args(v)...)
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	return ExitOK // unreachable on success: the process image is gone
}

// runProvision runs sshx.Provision (which does the actual work and writes
// last-provision.log) in the background while polling that same file and
// copying new bytes to stdout, so the CLI shows live output without any
// duplicated provisioning logic.
func runProvision(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: provision:", err)
		return ExitFail
	}
	if v.Mode == "cloud" {
		// cloud-init's packages: list only runs at first boot, baked into
		// the seed when the overlay was created, there is nothing left for
		// ssh-based provisioning to do, and piping a recipe (which for cloud
		// VMs is #cloud-config YAML, not a shell script) into `sh -s` would
		// just fail.
		fmt.Fprintf(stdout, "%s is a cloud VM: recipes are applied automatically via cloud-init at first boot; recreate the VM to change them.\n", a.VM)
		return ExitOK
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "provisioning %s...\n", a.VM)
	}

	// No cancellation source reaches here yet: runProvision has no signal
	// handling of its own, so this is a call site noted for the caller to
	// decide whether Ctrl-C should cancel an in-flight provision, not a
	// design decision made here.
	logPath := filepath.Join(v.Dir, "last-provision.log")
	done := make(chan error, 1)
	go func() { done <- sshx.Provision(context.Background(), v) }()

	if perr := streamFile(logPath, stdout, done); perr != nil {
		fmt.Fprintln(stderr, "stoat: provision:", perr)
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s provisioned\n", a.VM)
	return ExitOK
}

// streamFile copies newly-appended bytes of path to out every tick until
// done fires, then does one final copy so nothing written just before
// completion is missed.
func streamFile(path string, out io.Writer, done <-chan error) error {
	var offset int64
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			offset = copyNew(path, out, offset)
			return err
		case <-ticker.C:
			offset = copyNew(path, out, offset)
		}
	}
}

func copyNew(path string, out io.Writer, offset int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() <= offset {
		return offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	io.Copy(out, f)
	return fi.Size()
}
