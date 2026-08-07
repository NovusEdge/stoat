package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
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
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			// active is true for a showing: what is on disk is what is live,
			// unless the VM is stopped, in which case nothing is live at all.
			return a.ok(stdout, map[string]any{
				"vm":       a.VM,
				"forwards": wire.FromPortForwards(v.Forwards),
				"active":   v.State == core.StateRunning,
			})
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
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		// applies_at is the field that must exist: "saved but not live yet"
		// must never be readable as "refused", which is the bug core.Forward's
		// own doc comment warns about.
		appliesAt := "now"
		if !active {
			appliesAt = "next_start"
		}
		return a.ok(stdout, map[string]any{
			"vm":         a.VM,
			"forwards":   wire.FromPortForwards(a.Forwards),
			"active":     active,
			"applies_at": appliesAt,
		})
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
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"dry_run": a.Prune.DryRun,
			"items":   wire.FromPruneItems(removed),
		})
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

// prunePrefix renders a core.PruneItem.Class as a human-readable prefix.
// core returns the class as data (§3.3's PruneItem); the CLI, the only
// human-facing renderer, owns this text.
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
	// action names which of the three this was, for the wire. Only the matching
	// case runs; an empty action means no tag was given, so this is a listing.
	var action string
	var actErr error
	switch {
	case a.Restore:
		action, actErr = "restore", core.Restore(a.VM, a.Tag)
	case a.Delete:
		action, actErr = "delete", core.DeleteSnapshot(a.VM, a.Tag)
	case a.Tag != "":
		action, actErr = "save", core.TakeSnapshot(a.VM, a.Tag)
	}
	if action != "" {
		if actErr != nil {
			return a.fail(stdout, stderr, actErr)
		}
		if a.JSON {
			return a.ok(stdout, map[string]any{"vm": a.VM, "tag": a.Tag, "action": action})
		}
		if !a.Quiet {
			switch action {
			case "restore":
				fmt.Fprintf(stdout, "%s restored to %s\n", a.VM, a.Tag)
			case "delete":
				fmt.Fprintf(stdout, "deleted %s\n", a.Tag)
			case "save":
				fmt.Fprintf(stdout, "saved %s\n", a.Tag)
			}
		}
		return ExitOK
	}

	snaps, err := core.Snapshots(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"vm": a.VM, "snapshots": wire.FromSnapshots(snaps)})
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

// runDoctor prints core.Doctor's findings: the same checks the installer's
// pre-install checklist runs (qemu-system-x86_64, qemu-img, ssh, xorriso,
// /dev/kvm), so `stoat doctor` and `just setup` agree on host readiness.
//
// It prints the fix command when there is one, so a failed check tells the
// user how to repair it instead of leaving them to guess.
func runDoctor(a *Args, stdout, stderr io.Writer) int {
	checks := core.Doctor()
	var failed []core.HostCheck
	for _, c := range checks {
		if !c.OK {
			failed = append(failed, c)
		}
	}
	if a.JSON {
		// healthy, not ok: the envelope already owns "ok", and two
		// differently-scoped ok fields one level apart is a trap. Exit is 0
		// even when the host is unhealthy (§5): doctor SUCCEEDED at checking,
		// and exit 1 means stoat failed to answer, not that the answer was no.
		return a.ok(stdout, map[string]any{
			"healthy": len(failed) == 0,
			"checks":  wire.FromHostChecks(checks),
		})
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
