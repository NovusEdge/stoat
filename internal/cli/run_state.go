package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/core"
)

// parseForwards reads "8080:80" pairs, host port first: the spelling docker
// and ssh -L both use, so the ordering is the one a user already has in their
// fingers. Getting it backwards silently binds the wrong port, so the error
// names the whole offending argument rather than just complaining about a
// number.
func parseForwards(pairs []string) ([]core.PortForward, error) {
	var out []core.PortForward
	for _, p := range pairs {
		host, guest, ok := strings.Cut(p, ":")
		if !ok {
			return nil, fmt.Errorf("%q is not a HOST:GUEST port pair", p)
		}
		h, err := strconv.Atoi(strings.TrimSpace(host))
		if err != nil {
			return nil, fmt.Errorf("%q: host port %q is not a number", p, host)
		}
		g, err := strconv.Atoi(strings.TrimSpace(guest))
		if err != nil {
			return nil, fmt.Errorf("%q: guest port %q is not a number", p, guest)
		}
		out = append(out, core.PortForward{HostPort: h, GuestPort: g})
	}
	return out, nil
}

// runForward shows, sets, or clears a VM's port forwards. core.Forward reports
// whether they are live NOW; when they are not, saying so is the whole point:
// a user who declared a forward on a running VM and got silence would conclude
// it was working and spend the next ten minutes debugging the guest.
func runForward(a *Args, stdout, stderr io.Writer) int {
	if len(a.Forwards) == 0 && !a.Clear {
		v, err := core.Get(a.VM)
		if err != nil {
			fmt.Fprintln(stderr, "stoat: forward:", err)
			return ExitFail
		}
		if len(v.Forwards) == 0 {
			fmt.Fprintf(stdout, "%s has no port forwards\n", a.VM)
			return ExitOK
		}
		for _, f := range v.Forwards {
			fmt.Fprintf(stdout, "%d:%d\n", f.HostPort, f.GuestPort)
		}
		return ExitOK
	}

	active, err := core.Forward(a.VM, a.Forwards)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: forward:", err)
		return ExitFail
	}
	if a.Quiet {
		return ExitOK
	}
	switch {
	case a.Clear:
		fmt.Fprintf(stdout, "cleared %s's port forwards\n", a.VM)
	default:
		for _, f := range a.Forwards {
			fmt.Fprintf(stdout, "%d:%d\n", f.HostPort, f.GuestPort)
		}
	}
	if !active {
		fmt.Fprintf(stdout, "%s is running; this takes effect at next start\n", a.VM)
	}
	return ExitOK
}

// runPrune reports by default and deletes only with --apply. It prints what it
// would remove either way, so the dry run and the real run are readable as the
// same list.
func runPrune(a *Args, stdout, stderr io.Writer) int {
	removed, err := core.Prune(a.Prune)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: prune:", err)
		return ExitFail
	}
	if len(removed) == 0 {
		fmt.Fprintln(stdout, "nothing to prune")
		return ExitOK
	}
	for _, r := range removed {
		fmt.Fprintln(stdout, prunePrefix(r.Class)+r.Path)
	}
	if a.Prune.DryRun {
		fmt.Fprintln(stdout, "\n(dry run: nothing was deleted; re-run with --apply)")
	}
	return ExitOK
}

// prunePrefix renders a core.PruneItem.Class as the human-readable prefix
// core.Prune used to hard-code into its return strings. core now returns the
// class separately (docs/design/json-contract-draft.md §3.3's PruneItem), so
// the CLI, the only human-facing renderer, owns this text.
func prunePrefix(class string) string {
	switch class {
	case "broken_vm":
		return "broken vm: "
	case "partial_download":
		return "partial download: "
	case "orphaned_image":
		return "orphaned image: "
	default:
		return class + ": "
	}
}

// runSnapshot lists, saves, restores or deletes a snapshot. Restoring is the
// one genuinely destructive path here: everything since the snapshot is
// discarded with no second copy, so it says what it did rather than
// succeeding silently.
func runSnapshot(a *Args, stdout, stderr io.Writer) int {
	switch {
	case a.Restore:
		if err := core.Restore(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "%s restored to %s\n", a.VM, a.Tag)
		}
		return ExitOK
	case a.Delete:
		if err := core.DeleteSnapshot(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "deleted %s\n", a.Tag)
		}
		return ExitOK
	case a.Tag != "":
		if err := core.TakeSnapshot(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "saved %s\n", a.Tag)
		}
		return ExitOK
	}

	snaps, err := core.Snapshots(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: snapshot:", err)
		return ExitFail
	}
	if len(snaps) == 0 {
		fmt.Fprintf(stdout, "%s has no snapshots\n", a.VM)
		return ExitOK
	}
	fmt.Fprintf(stdout, "%-24s %-10s %-20s %s\n", "TAG", "SIZE", "CREATED", "RAM")
	for _, s := range snaps {
		ram := "no"
		if s.VMState {
			ram = "yes"
		}
		fmt.Fprintf(stdout, "%-24s %-10s %-20s %s\n", s.Tag, s.Size, s.Created, ram)
	}
	return ExitOK
}

// runDoctor prints core.Doctor's findings. It checks strictly more than it
// used to: this was two ad-hoc probes (qemu.Preflight plus a bare ssh
// LookPath), while core.Doctor runs the same set the installer's pre-install
// checklist does (qemu-system-x86_64, qemu-img, ssh, xorriso and /dev/kvm),
// so `stoat doctor` and `just setup` can no longer disagree about whether the
// host is ready.
//
// It also prints the fix command when there is one. The installer has always
// told the user how to repair a failed check; the CLI made them guess.
func runDoctor(a *Args, stdout, stderr io.Writer) int {
	var failed []core.HostCheck
	for _, c := range core.Doctor() {
		if !c.OK {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		fmt.Fprintln(stdout, "ok")
		return ExitOK
	}
	for _, c := range failed {
		fmt.Fprintf(stdout, "FAIL: %s: %s\n", c.Name, c.Detail)
		if len(c.Fix) > 0 {
			fmt.Fprintf(stdout, "      try: %s\n", strings.Join(c.Fix, " "))
		}
	}
	return ExitFail
}
