package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
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
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		direction := "from_guest"
		if a.ToRemote {
			direction = "to_guest"
		}
		return a.ok(stdout, map[string]any{
			"vm": a.VM, "direction": direction, "local": a.Local, "remote": a.Remote,
		})
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
// stoat's own exit codes and the guest's share one range, so a guest command
// exiting 2 is indistinguishable from a stoat usage error. Remapping the
// guest's status would misrepresent what the command did. ssh takes the
// same trade.
//
// stoat's OWN failures here (no such VM, not running) still exit 1 and
// print to stderr, distinguishable from guest output.
func runExec(a *Args, stdout, stderr io.Writer) int {
	// No timeout: a guest command may legitimately run for hours, and ^C
	// already kills the ssh process. Bounding it here would mean inventing a
	// limit nobody asked for.
	res, err := core.Exec(context.Background(), a.VM, a.Command)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		// Under --json the process exits 0 whenever the command RAN, whatever it
		// returned (§5): the guest's status is already in exit_code, and
		// conflating it with stoat's own leaves a consumer unable to tell a
		// guest exiting 2 from a stoat usage error.
		//
		// Embedded, not spread into a map: the DTO's omitempty is what keeps
		// stdout_base64 absent for the ordinary UTF-8 case, and a map has no
		// way to say that.
		return a.ok(stdout, struct {
			VM string `json:"vm"`
			wire.ExecResult
		}{VM: a.VM, ExecResult: wire.FromExecResult(res)})
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
	if a.JSON {
		// syscall.Exec destroys the process image, so there is no "after" in
		// which to write a result line. Refusing is the only honest answer;
		// faking one would break the "exactly one terminal result" guarantee
		// for every consumer that ever calls it.
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd,
			wire.UsageError("ssh replaces the stoat process and cannot emit a result; use `stoat --json exec "+a.VM+" ...` for a command, or ssh_port/ssh_user from `stoat --json ls`"))
		return ExitUsage
	}
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
		return a.fail(stdout, stderr, err)
	}
	if v.Mode == "cloud" {
		// cloud-init's packages: list runs only at first boot, baked into
		// the seed when the overlay was created. There is nothing left for
		// ssh-based provisioning to do, and a cloud recipe is #cloud-config
		// YAML, not a shell script, so piping it into `sh -s` would fail.
		const reason = "cloud VM: recipes are applied by cloud-init at first boot; recreate the VM to change them"
		if a.JSON {
			// A consumer has to be able to tell "recipes ran" from "there was
			// nothing to run" without reading English prose.
			return a.ok(stdout, map[string]any{
				"vm": a.VM, "provisioned": false, "skipped_reason": reason,
			})
		}
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

	// Under --json the recipe's raw bytes must not reach stdout: they would sit
	// in the middle of the JSON Lines stream and every consumer's json.loads
	// would fail on them. Each appended line becomes a "log" event instead.
	out := stdout
	var lw *jsonLogWriter
	if a.JSON {
		lw = &jsonLogWriter{em: wire.NewEmitter(stdout), cmd: a.Cmd}
		out = lw
	}
	perr := streamFile(logPath, out, done)
	if lw != nil {
		lw.Flush()
	}
	if perr != nil {
		return a.fail(stdout, stderr, perr)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm": a.VM, "provisioned": true, "skipped_reason": "",
		})
	}
	fmt.Fprintf(stdout, "%s provisioned\n", a.VM)
	return ExitOK
}

// jsonLogWriter wraps appended log bytes as one "log" event per line.
type jsonLogWriter struct {
	em  *wire.Emitter
	cmd string
	buf []byte
}

func (w *jsonLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits a trailing line that never got its newline. A recipe killed
// mid-write leaves exactly that, and it is the line that says why.
func (w *jsonLogWriter) Flush() {
	if len(w.buf) > 0 {
		w.line(string(w.buf))
		w.buf = nil
	}
}

func (w *jsonLogWriter) line(s string) {
	s = strings.TrimSuffix(s, "\r")
	// Provision marks each recipe boundary in the stream, so a consumer gets
	// real per-recipe progress instead of a percentage nobody can compute.
	if name, ok := sshx.ParseRecipeMarker(s); ok {
		_ = w.em.Event(wire.TypeStage, w.cmd, map[string]any{"recipe": name})
	}
	_ = w.em.Event(wire.TypeLog, w.cmd, map[string]any{"line": s})
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
