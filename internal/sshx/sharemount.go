package sshx

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/config"
)

// guestFstabPath is the guest's real fstab. shareMountScript takes it as a
// parameter so a test can point at a scratch file instead.
const guestFstabPath = "/etc/fstab"

// shareMountTags returns the 9p shares a disk-installed guest must mount for
// v, or nil if none apply.
//
// A live VM already mounts through the apkovl overlay's own fstab (see
// apkovl.Build). A cloud VM seeds its mounts through cloud-init instead. So
// this only fires for Mode "disk", and only when Share is configured: with
// no Share there is no host mount to add, by this feature's design.
//
// qemu.Args attaches the work virtfs to every VM unconditionally, so a disk
// VM gets both tags once Share makes this step run at all.
func shareMountTags(v *config.VM) []apkovl.Mount9p {
	if v.Mode != "disk" || v.Share == "" {
		return nil
	}
	return []apkovl.Mount9p{apkovl.HostMount9p, apkovl.WorkMount9p}
}

// fstabEnsureLine returns the sh fragment that appends m's fstab line to
// fstabPath, only if a line for m.Dir is not already there. grep's pattern
// anchors on "<tag> " so a substring match (e.g. "work" inside "network")
// can't produce a false positive.
func fstabEnsureLine(m apkovl.Mount9p, fstabPath string) string {
	return fmt.Sprintf(
		"grep -q '^%s %s ' %s 2>/dev/null || printf '%%s' %s >> %s\n",
		m.Tag, m.Dir, fstabPath, shQuote(m.FstabLine()), fstabPath,
	)
}

// mountIfNeeded returns the sh fragment that creates m's mountpoint and
// mounts it if not already mounted, echoing the outcome for the provision
// log. It does not use `set -e`: a failed mount must not stop the script,
// since the share is best-effort, matching the overlay's own nofail option.
func mountIfNeeded(m apkovl.Mount9p) string {
	dir := shQuote(m.Dir)
	return fmt.Sprintf(
		"mkdir -p %s\n"+
			"if mountpoint -q %s; then\n"+
			"  echo \"stoat: %s share already mounted\"\n"+
			"elif mount %s; then\n"+
			"  echo \"stoat: mounted %s share\"\n"+
			"else\n"+
			"  echo \"stoat: could not mount %s share (continuing)\"\n"+
			"fi\n",
		dir, dir, m.Tag, dir, m.Tag, m.Tag,
	)
}

// shQuote wraps s in single quotes for sh, escaping any single quote it
// contains. m.Dir and m.Tag come from this package's own Mount9p values, not
// user input, but the recipe body is piped to `sh -s` the same way, so this
// keeps the same discipline.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// repairNetdevLine returns the sh fragment that strips _netdev from an
// existing 9p line in fstabPath. A VM installed before the option was dropped
// still carries _netdev on disk, and openrc's localmount skips such lines, so
// the share stops mounting on every boot after the first. The sed is a no-op
// on a line that never had it.
func repairNetdevLine(fstabPath string) string {
	return fmt.Sprintf("sed -i 's/,_netdev,nofail/,nofail/g' %s 2>/dev/null || true\n", fstabPath)
}

// shareMountScript returns the full sh script that idempotently mounts every
// tag in tags, best-effort. fstabPath lets a test point at a scratch fstab;
// production always passes guestFstabPath.
func shareMountScript(tags []apkovl.Mount9p, fstabPath string) string {
	if len(tags) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(repairNetdevLine(fstabPath))
	for _, m := range tags {
		b.WriteString(fstabEnsureLine(m, fstabPath))
		b.WriteString(mountIfNeeded(m))
	}
	return b.String()
}

// mountShares runs shareMountScript over ssh for v, if shareMountTags finds
// any tags. It writes to log the same way a recipe does, and never returns
// an error: the share is best-effort, so a failure here must not stop
// Provision from running the recipes that follow.
func mountShares(ctx context.Context, v *config.VM, log io.Writer) {
	tags := shareMountTags(v)
	if len(tags) == 0 {
		return
	}
	fmt.Fprintln(log, "\n=== mounting 9p shares ===")
	cmd := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, []string{"sh", "-s"})...)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = recipeShutdownGrace
	cmd.Stdin = strings.NewReader(shareMountScript(tags, guestFstabPath))
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "stoat: share mount step failed, continuing: %v\n", err)
	}
}
