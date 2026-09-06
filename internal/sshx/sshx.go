// Package sshx runs commands inside guests over the forwarded SSH port.
package sshx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

// WaitTimeout is how long Provision waits for sshd after a start. A first
// live boot plus dhcp is slower than a warm one, so this is generous.
const WaitTimeout = 90 * time.Second

// User returns the account to ssh into v as: v.SSHUser when it was recorded
// at build time (the catalog entry's account, or cloudinit.User for a cloud
// image), otherwise "root".
//
// It does not fall back to guest.DefaultSSHUser. An empty v.SSHUser is not
// "unknown": the cloudinit backend always seeds a real account, so it is
// empty only for the apkovl and ssh backends, both unlocked-root images (a
// live Alpine apkovl, or a BYO disk image awaiting a manual install).
// Guessing the registry default there would be wrong. A BYO file can be
// labelled e.g. "ubuntu" via iso.Infer or a form override while still going
// through the ssh backend, with no cloud-init and no seeded account, so that
// image has no "stoat" user, only whatever the installer itself created.
// See form.go's resolvedSSHUser for the one place that decides the recorded
// value.
func User(v *config.VM) string {
	if v.SSHUser != "" {
		return v.SSHUser
	}
	return "root"
}

// connOptions returns the connection settings ssh(1) and scp(1) both accept
// with identical syntax: host key checking, connect behaviour, and the
// identity file. Keeping them in one place means a caller building an scp
// invocation (core.CopyTo/CopyFrom) never has to re-decide
// StrictHostKeyChecking or find the private key path a second time.
//
// The port flag is not here. ssh takes "-p" and scp takes "-P" (capital,
// since scp's lowercase -p means "preserve file times"), so each caller
// supplies it itself. See CopyArgs.
func connOptions() []string {
	return []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-i", keys.PrivatePath(),
	}
}

// Args returns the argv (excluding argv[0]) for ssh into v. Host key checks
// are off on purpose: this is a loopback forward to a VM stoat just built,
// and live VMs are recreated constantly.
func Args(v *config.VM, extra ...string) []string {
	a := append([]string{"-p", fmt.Sprint(v.SSHPort)}, connOptions()...)
	a = append(a, User(v)+"@127.0.0.1")
	return append(a, extra...)
}

// escalate prefixes remote with the guest's escalate argv for a non-root ssh
// user. Cloud images log in as an unprivileged account with passwordless
// sudo from the seed; apkovl and BYO-root images connect as root and get no
// prefix. An unknown guest falls back to sudo, the only answer that has ever
// been right for a cloud image.
func escalate(v *config.VM, remote []string) []string {
	if User(v) == "root" {
		return remote
	}
	prefix := []string{"sudo", "-n"}
	if o, ok := guest.Lookup(v.OS); ok && len(o.Escalate) > 0 {
		prefix = o.Escalate
	}
	return append(append([]string{}, prefix...), remote...)
}

// preludeFor renders the guest prelude for v, or "" for a guest stoat does
// not know, in which case a recipe runs bare as it always did.
func preludeFor(v *config.VM, runtime string) string {
	o, ok := guest.Lookup(v.OS)
	if !ok {
		return ""
	}
	return guest.Prelude(o, runtime)
}

// CopyArgs returns the argv (excluding argv[0]) for scp between the host and
// v's guest. It shares every connection setting Args does (see connOptions)
// and differs only in the port flag, since scp's is capital -P.
//
// toRemote picks the direction. true puts the guest spec
// ("user@127.0.0.1:remotePath") on the right, as scp's destination
// (core.CopyTo). false puts it on the left, as scp's source (core.CopyFrom).
// localPath is always a bare host path, never quoted or rewritten: it is
// scp's own argv element, not something a shell re-parses.
//
// -q suppresses scp's interactive progress meter. This argv is built for
// exec.CommandContext, never a terminal, so stray meter output would
// otherwise get captured as if it were an error.
func CopyArgs(v *config.VM, localPath, remotePath string, toRemote bool) []string {
	a := append([]string{"-P", fmt.Sprint(v.SSHPort), "-q"}, connOptions()...)
	remoteSpec := User(v) + "@127.0.0.1:" + remotePath
	if toRemote {
		return append(a, localPath, remoteSpec)
	}
	return append(a, remoteSpec, localPath)
}

// Wait blocks until the forwarded port accepts a connection, ctx is done, or
// timeout elapses, whichever comes first. timeout is a real ceiling even
// for a ctx with no deadline of its own, matching WaitTimeout's role in
// Provision below.
//
// ctx is honoured at two points, not just between attempts. The dial itself
// runs under DialContext, so a cancellation lands as soon as the connect
// syscall returns. The sleep between attempts is a select against
// ctx.Done(), not a bare time.Sleep. internal/core/wait.go's
// waitReachable/sshBannerUp run the same DialContext-based banner check
// independently of this one: core.Wait needs to keep going after ctx
// expires elsewhere, sshx.Wait needs a caller-supplied timeout ceiling, and
// duplicating the ~10-line dial is cheaper than reconciling those two
// different contracts.
func Wait(ctx context.Context, v *config.VM, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", v.SSHPort)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		dialTimeout := time.Second
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		c, err := dialCtx(ctx, addr, dialTimeout)
		if err == nil {
			if bannerReady(c, time.Until(deadline)) {
				return nil
			}
			_ = c.Close()
		} else if ctx.Err() != nil {
			return ctx.Err()
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := 500 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return fmt.Errorf("%s: ssh not reachable on port %d after %s", v.Name, v.SSHPort, timeout)
}

// dialCtx dials addr, bounding the attempt by whichever of ctx or
// perAttempt's own timeout fires first. A plain context.WithTimeout(ctx,
// perAttempt) would do the same, but this avoids the extra cancel func at
// every call site inside Wait's loop.
func dialCtx(ctx context.Context, addr string, perAttempt time.Duration) (net.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, perAttempt)
	defer cancel()
	var d net.Dialer
	return d.DialContext(dctx, "tcp", addr)
}

// bannerDeadline is the per-attempt read deadline for the SSH identification
// banner. A guest sshd forking under load on a 1-vCPU VM mid-boot can take
// longer than a couple hundred milliseconds to emit its banner. Each retry
// in Wait opens a fresh connection, so a too-short deadline here is a hard
// cliff no amount of retrying can cross. The outer WaitTimeout already
// bounds the whole operation, so a generous per-attempt deadline costs
// nothing in the failure case.
const bannerDeadline = 2 * time.Second

// bannerReady reports whether c is a real sshd, not just an accepted TCP
// connection. QEMU/libslirp's user-mode networking accepts the host-side
// socket at device init and only later dials the guest, tearing the
// connection down if nothing answers yet. A bare accept() does not mean
// sshd is up; requiring the "SSH-" identification banner does.
//
// budget caps the read deadline at the remaining overall timeout, so a
// short-lived caller (e.g. Wait(v, 300*time.Millisecond) in a test) still
// returns promptly instead of blocking for the full bannerDeadline.
func bannerReady(c net.Conn, budget time.Duration) bool {
	d := bannerDeadline
	if budget < d {
		d = budget
	}
	_ = c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 4)
	_, err := io.ReadFull(c, buf)
	return err == nil && string(buf) == "SSH-"
}

// recipeShutdownGrace is how long a recipe's ssh process gets to exit after
// ctx is cancelled before it is killed outright. cmd.Cancel below sends
// SIGTERM instead of exec.CommandContext's default SIGKILL, so ssh gets the
// chance to close its session cleanly and let a remote shell it started
// react. This bounds that grace period rather than waiting on it forever.
const recipeShutdownGrace = 5 * time.Second

// healthShutdownGrace is deliberately short: a health probe has no recipe
// state to preserve after its context expires, and a TERM-ignoring probe must
// not extend Wait's single health budget by the normal recipe grace period.
const healthShutdownGrace = 100 * time.Millisecond

// cloudInitStatus is the stable subset of `cloud-init status --format json`.
// cloud-init adds informational fields over time; readiness only depends on
// the terminal status and hard-error list. Recoverable errors remain available
// for a degraded diagnostic without making a warning block provisioning.
type cloudInitStatus struct {
	Status            string              `json:"status"`
	ExtendedStatus    string              `json:"extended_status"`
	Errors            []string            `json:"errors"`
	RecoverableErrors map[string][]string `json:"recoverable_errors"`
}

func parseCloudInitStatus(body []byte) (cloudInitStatus, error) {
	var status cloudInitStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return cloudInitStatus{}, fmt.Errorf("invalid cloud-init status JSON: %w", err)
	}
	if status.Status == "" {
		return cloudInitStatus{}, fmt.Errorf("cloud-init status JSON has no status")
	}
	return status, nil
}

// cloudInitPollInterval spaces the readiness probes. `cloud-init status`
// answers at once, so the interval is the whole cost of a probe.
const cloudInitPollInterval = 5 * time.Second

// cloudInitProbeTimeout bounds one probe. A guest that stops answering must
// not wedge the loop that owns the deadline.
const cloudInitProbeTimeout = time.Minute

// cloudInitReadinessBudget bounds the wait when the caller gives no deadline.
// A cloud image that installs a large package set takes minutes, and every
// recipe in the seed runs before cloud-init reports done.
const cloudInitReadinessBudget = 30 * time.Minute

// sshTransportExit is ssh's own failure code. The guest never returns it, so
// it means the connection failed rather than the command.
const sshTransportExit = 255

// cloudInitProbe asks the guest for cloud-init's status once. It asks as the
// seeded SSH account first, because that account exists before cloud-init
// installs the guest's sudo package. cloud-init 25.3 keeps /run/cloud-init at
// mode 0700, so the unprivileged answer there is a Python traceback and no
// JSON; the escalated retry covers that guest. The exit code carries meaning
// beside the JSON, so a body that parses wins over any status the command
// exited with.
func cloudInitProbe(ctx context.Context, v *config.VM, log io.Writer) (cloudInitStatus, int, error) {
	attempt := func(argv []string) (cloudInitStatus, int, error) {
		probeCtx, cancel := context.WithTimeout(ctx, cloudInitProbeTimeout)
		defer cancel()

		var out bytes.Buffer
		ci := exec.CommandContext(probeCtx, "ssh", Args(v, argv...)...)
		ci.Cancel = func() error { return ci.Process.Signal(syscall.SIGTERM) }
		ci.WaitDelay = recipeShutdownGrace
		ci.Stdout = &out
		ci.Stderr = log

		runErr := ci.Run()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return cloudInitStatus{}, 0, ctxErr
		}
		exitCode := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) {
				return cloudInitStatus{}, 0, fmt.Errorf("cloud-init readiness command failed: %w", runErr)
			}
			exitCode = exitErr.ExitCode()
		}
		status, err := parseCloudInitStatus(out.Bytes())
		if err != nil {
			return cloudInitStatus{}, exitCode, err
		}
		return status, exitCode, nil
	}

	remote := []string{"cloud-init", "status", "--format", "json"}
	status, exitCode, err := attempt(remote)
	if err == nil || ctx.Err() != nil || exitCode == sshTransportExit {
		return status, exitCode, err
	}
	escalated := escalate(v, remote)
	if slices.Equal(escalated, remote) {
		return status, exitCode, err
	}
	return attempt(escalated)
}

// waitCloudInit waits for cloud-init to reach a terminal status. cloud-init's
// own `status --wait` cannot be trusted to return: on a guest whose run
// directory it has already made root-only, the unprivileged wait loop never
// observes the result. Stoat therefore owns the deadline and polls.
//
// Exit 2 is cloud-init's recoverable degraded result; the JSON still has to
// say done and list no hard errors before provisioning may continue.
func waitCloudInit(ctx context.Context, v *config.VM, log io.Writer) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cloudInitReadinessBudget)
		defer cancel()
	}

	last := "no answer yet"
	for {
		status, exitCode, err := cloudInitProbe(ctx, v, log)
		switch {
		case ctx.Err() != nil:
		case exitCode == sshTransportExit:
			return fmt.Errorf("cloud-init readiness command exited with status %d", exitCode)
		case err != nil:
			last = err.Error()
		case status.Status == "error":
			if len(status.Errors) > 0 {
				return fmt.Errorf("cloud-init reported hard errors: %s", strings.Join(status.Errors, "; "))
			}
			return fmt.Errorf("cloud-init is not ready: status %q", status.Status)
		case status.Status == "done":
			if len(status.Errors) > 0 {
				return fmt.Errorf("cloud-init reported hard errors: %s", strings.Join(status.Errors, "; "))
			}
			if exitCode == 2 {
				fmt.Fprintf(log, "cloud-init completed with recoverable warnings: %v\n", status.RecoverableErrors)
			}
			return nil
		default:
			last = fmt.Sprintf("status %q", status.Status)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("cloud-init did not report done (%s): %w", last, ctx.Err())
		case <-time.After(cloudInitPollInterval):
		}
	}
}

// RunCheck runs one command inside v's guest through the guest prelude, as
// the recipe's ssh user and under the guest's escalation. The command is
// sent over stdin so it does not become a local ssh argv element.
func RunCheck(ctx context.Context, v *config.VM, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var prelude string
	if o, ok := guest.Lookup(v.OS); ok {
		prelude = guest.Prelude(o, "sh")
	}
	body := prelude + "\n" + command + "\n"
	cmd := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, []string{"sh", "-s"})...)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = healthShutdownGrace
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Provision runs each of v's recipes over ssh, streaming output to
// last-provision.log. The detail view tails that file on a ticker, so there
// is no channel plumbing between this and the UI.
//
// ctx cancels both phases: the initial wait for sshd (Wait is itself
// ctx-aware) and each recipe's ssh process. Each recipe runs under
// exec.CommandContext, so a cancelled apply actually kills ssh rather than
// leaving it running against the guest after Provision has returned.
func Provision(ctx context.Context, v *config.VM) (err error) {
	logx.L().Info("provision start", "vm", v.Name, "recipes", strings.Join(v.Recipes, ","))
	defer func() {
		if err != nil {
			logx.L().Error("provision failed", "vm", v.Name, "err", err)
		} else {
			logx.L().Info("provision done", "vm", v.Name)
		}
	}()

	log, err := os.Create(v.ProvisionLogPath())
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	fmt.Fprintf(log, "waiting for ssh on port %d…\n", v.SSHPort)
	if err := Wait(ctx, v, WaitTimeout); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(log, "CANCELLED: %v\n", err)
		} else {
			fmt.Fprintf(log, "FAILED: %v\n", err)
		}
		return err
	}

	// A cloud image runs cloud-init at first boot; it may still be installing
	// packages when sshd comes up. A recipe that calls apt or dnf then races
	// cloud-init for the package-manager lock and fails. Wait for cloud-init
	// to finish before mounts, package setup, or recipes. apkovl and BYO images
	// ship no cloud-init and skip this.
	if v.Backend == "cloudinit" {
		fmt.Fprintln(log, "waiting for cloud-init to finish…")
		if err := waitCloudInit(ctx, v, log); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				fmt.Fprintf(log, "CANCELLED: %v\n", ctxErr)
				return ctxErr
			}
			fmt.Fprintf(log, "FAILED: cloud-init readiness: %v\n", err)
			return fmt.Errorf("cloud-init readiness: %w", err)
		}
	}

	mountShares(ctx, v, log)

	if o, ok := guest.Lookup(v.OS); ok && strings.TrimSpace(o.Pkg.Setup) != "" {
		fmt.Fprintln(log, "refreshing the package index...")
		st := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, []string{"sh", "-s"})...)...)
		st.Cancel = func() error { return st.Process.Signal(syscall.SIGTERM) }
		st.WaitDelay = recipeShutdownGrace
		st.Stdin = strings.NewReader(guest.Prelude(o, "sh") + "stoat_pkg_setup\n")
		st.Stdout = log
		st.Stderr = log
		if err := st.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				fmt.Fprintf(log, "CANCELLED: %v\n", ctxErr)
				return ctxErr
			}
			fmt.Fprintf(log, "FAILED: package index refresh: %v\n", err)
			return fmt.Errorf("package index refresh: %w", err)
		}
	}

	for _, name := range v.Recipes {
		// Checked before each recipe, not left to cmd.Run below alone: a ctx
		// cancelled between recipes must stop here rather than start one more
		// ssh process it will only have to kill.
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(log, "CANCELLED: %v\n", err)
			return err
		}

		body, err := recipes.ScriptBody(name, v.OS)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		runtime, err := recipes.RuntimeFor(name, v.OS)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		fmt.Fprintf(log, "\n%s\n", RecipeMarker(name))
		m, haveManifest, err := recipes.ManifestFor(name)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		input, secrets, err := recipeInput(v, name, m, haveManifest, runtime, body)
		if err != nil {
			redacted := redactString(err.Error(), secrets)
			fmt.Fprintf(log, "FAILED: recipe %s: %s\n", name, redacted)
			return fmt.Errorf("recipe %s: %s", name, redacted)
		}
		redactor := newRedactingWriter(log, secrets)

		if bootstrap := recipes.BootstrapScript(runtime, v.OS); bootstrap != "" {
			fmt.Fprintf(log, "ensuring %s is installed...\n", runtime)
			bs := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, []string{"sh", "-s"})...)...)
			bs.Cancel = func() error { return bs.Process.Signal(syscall.SIGTERM) }
			bs.WaitDelay = recipeShutdownGrace
			bs.Stdin = strings.NewReader(guest.WithPrelude(bootstrap, preludeFor(v, "sh")))
			bs.Stdout = log
			bs.Stderr = log
			if err := bs.Run(); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					fmt.Fprintf(log, "CANCELLED: recipe %s: %v\n", name, ctxErr)
					return ctxErr
				}
				fmt.Fprintf(log, "FAILED: recipe %s: installing %s: %v\n", name, runtime, err)
				return fmt.Errorf("recipe %s: installing %s: %w", name, runtime, err)
			}
		}

		cmd := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, recipes.InterpreterArgs(runtime))...)...)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = recipeShutdownGrace
		cmd.Stdin = strings.NewReader(input)
		cmd.Stdout = redactor
		cmd.Stderr = redactor
		if err := cmd.Run(); err != nil {
			_ = redactor.Flush()
			// ctx being the cause is reported as CANCELLED, not a plain recipe
			// FAILED. waitApplied (internal/core/wait.go) treats only a final
			// "done" line as success, so either wording leaves the recipe
			// correctly "not applied". But a human reading the log, or a
			// caller sniffing Logs' text, must not read a cancellation as if
			// the recipe itself had failed.
			if ctxErr := ctx.Err(); ctxErr != nil {
				fmt.Fprintf(log, "CANCELLED: recipe %s: %s\n", name, redactString(ctxErr.Error(), secrets))
				return ctxErr
			}
			_ = redactor.Flush()
			redacted := redactString(err.Error(), secrets)
			fmt.Fprintf(log, "FAILED: recipe %s: %s\n", name, redacted)
			return fmt.Errorf("recipe %s: %s", name, redacted)
		}
		if err := redactor.Flush(); err != nil {
			return err
		}
		if haveManifest && m.Schema >= 3 {
			if err := collectOutputs(ctx, v, name, m, secrets, log); err != nil {
				redacted := redactString(err.Error(), secrets)
				fmt.Fprintf(log, "FAILED: recipe %s: reading outputs: %s\n", name, redacted)
				return fmt.Errorf("recipe %s: reading outputs: %s", name, redacted)
			}
		}
	}
	fmt.Fprintln(log, "\ndone")
	return nil
}

// recipeInput wraps a recipe with its resolved environment and per-recipe
// output file setup. Secret values travel through stdin and never through ssh
// argv. The Python form sets os.environ before the shared prelude runs.
func recipeInput(v *config.VM, name string, m recipes.Manifest, haveManifest bool, runtime, body string) (string, []string, error) {
	params := map[string]string{}
	secrets := []string{}
	if haveManifest && len(m.Params) > 0 {
		stored, err := config.LoadSecrets(v.Dir)
		if err != nil {
			return "", nil, err
		}
		resolved, err := recipes.Resolve(m, v.Params[name], stored[name])
		if err != nil {
			return "", nil, err
		}
		for param, value := range resolved {
			params[param] = value
			if m.Params[param].Type == "secret" {
				if value != "" {
					secrets = append(secrets, value)
				}
				continue
			}
		}
	}
	env := recipes.Env(name, params)
	path := OutputDir + "/" + name
	prelude := preludeFor(v, runtime)
	if runtime == "python3" {
		var b strings.Builder
		b.WriteString("import os\n")
		for _, kv := range env {
			key, value, _ := strings.Cut(kv, "=")
			fmt.Fprintf(&b, "os.environ[%q] = %q\n", key, value)
		}
		if !haveManifest || m.Schema < 3 {
			b.WriteString(guest.WithPrelude(body, prelude))
			return b.String(), secrets, nil
		}
		fmt.Fprintf(&b, "os.environ[\"STOAT_OUTPUT\"] = %q\n", path)
		fmt.Fprintf(&b, "os.makedirs(%q, mode=0o700, exist_ok=True)\n", OutputDir)
		b.WriteString("open(os.environ[\"STOAT_OUTPUT\"], \"w\").close()\n")
		b.WriteString(guest.WithPrelude(body, prelude))
		return b.String(), secrets, nil
	}
	var b strings.Builder
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&b, "export %s=%s\n", key, shellValue(value))
	}
	if !haveManifest || m.Schema < 3 {
		b.WriteString(guest.WithPrelude(body, prelude))
		return b.String(), secrets, nil
	}
	fmt.Fprintf(&b, "export STOAT_OUTPUT=%s\n", shellPath(path))
	fmt.Fprintf(&b, "mkdir -p %s && chmod 700 %s && : > \"$STOAT_OUTPUT\"\n", shellPath(OutputDir), shellPath(OutputDir))
	b.WriteString(guest.WithPrelude(body, prelude))
	return b.String(), secrets, nil
}

func shellValue(value string) string {
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			if !strings.ContainsRune(value, '"') {
				var b strings.Builder
				b.WriteByte('"')
				for _, r := range value {
					if r == '\\' || r == '$' || r == '`' {
						b.WriteByte('\\')
					}
					b.WriteRune(r)
				}
				b.WriteByte('"')
				return b.String()
			}
			return guest.ShQuote(value)
		}
	}
	return value
}

// shellPath leaves simple paths readable for command diagnostics and quotes
// every path containing shell syntax.
func shellPath(path string) string {
	for _, r := range path {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return guest.ShQuote(path)
		}
	}
	return path
}

// redactingWriter keeps a suffix between writes so a secret split across two
// process writes is removed before either part reaches the apply log.
type redactingWriter struct {
	dst     io.Writer
	secrets []string
	pending string
	keep    int
}

func newRedactingWriter(dst io.Writer, secrets []string) *redactingWriter {
	max := 0
	for _, secret := range secrets {
		if len(secret) > max {
			max = len(secret)
		}
	}
	if max > 0 {
		max--
	}
	return &redactingWriter{dst: dst, secrets: sortedSecrets(secrets), keep: max}
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	data := w.pending + string(p)
	cut := len(data) - w.keep
	if cut > 0 {
		for _, secret := range w.secrets {
			if secret == "" {
				continue
			}
			for start := strings.Index(data, secret); start >= 0; {
				end := start + len(secret)
				if start < cut && end > cut {
					cut = start
				}
				next := strings.Index(data[start+1:], secret)
				if next < 0 {
					break
				}
				start += next + 1
			}
		}
	}
	if cut < 0 {
		cut = 0
	}
	if _, err := io.WriteString(w.dst, redactString(data[:cut], w.secrets)); err != nil {
		return 0, err
	}
	w.pending = data[cut:]
	return len(p), nil
}

func (w *redactingWriter) Flush() error {
	if w.pending == "" {
		return nil
	}
	_, err := io.WriteString(w.dst, redactString(w.pending, w.secrets))
	w.pending = ""
	return err
}

func redactString(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}

// sortedSecrets provides stable redaction replacement order when secrets
// overlap, replacing the longest value first.
func sortedSecrets(secrets []string) []string {
	out := append([]string(nil), secrets...)
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
