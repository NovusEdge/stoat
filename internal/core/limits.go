package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/hostcheck"
	"github.com/novusedge/stoat/internal/project"
	"github.com/novusedge/stoat/internal/settings"
)

// ErrLimit is returned when a create or a start would cross a configured
// limit, or would ask the host for memory it does not have.
var ErrLimit = errors.New("limit reached")

// limitsFor resolves the account limits, lowered by the limits of the project
// that declared this VM. A VM created by `stoat new` has no project, so the
// account limits stand alone.
func limitsFor(dir string) settings.Limits {
	s, err := settings.Load()
	if err != nil {
		return settings.Limits{}
	}
	l := s.Limits
	if dir == "" {
		return l
	}
	p, err := project.Load(dir)
	if err != nil {
		// A project directory that no longer parses cannot lower anything.
		// The account limits still apply, which is the safe half.
		return l
	}
	return l.Tighten(p.Limits)
}

// CheckCreate refuses a new VM when the count limit is already met. It counts
// every VM in the data root, running or not: the limit answers "how many VMs
// may exist", which is the question an agent in a create loop is failing.
func CheckCreate(projectDir string) error {
	l := limitsFor(projectDir)
	if l.MaxVMs <= 0 {
		return nil
	}
	vms, err := config.List()
	if err != nil {
		return err
	}
	if len(vms) < l.MaxVMs {
		return nil
	}
	return fmt.Errorf("%w: %d vms exist and limits.max_vms is %d; `stoat rm` frees a slot, or raise the limit in %s",
		ErrLimit, len(vms), l.MaxVMs, settings.Path())
}

// CheckStart refuses a start that would exceed the RAM limit, or that the host
// cannot back with real memory.
//
// The caller holds the data-root lock. Counting running VMs and starting one
// are two steps, and two `up` calls in that gap would both see room.
func CheckStart(v *config.VM, force bool) error {
	if v.Provider != "" {
		// A GCE instance runs on Google's hardware. Neither the host's memory
		// nor a RAM limit meant for this machine describes it.
		return nil
	}
	running, total := runningQEMU()

	l := limitsFor(v.Project)
	if l.MaxRAMMB > 0 && total+v.RAM > l.MaxRAMMB {
		held := "nothing else runs"
		if len(running) > 0 {
			held = fmt.Sprintf("%d MB already runs (%s)", total, strings.Join(running, ", "))
		}
		return fmt.Errorf("%w: %s needs %d MB, %s, and limits.max_ram_mb is %d",
			ErrLimit, v.Name, v.RAM, held, l.MaxRAMMB)
	}

	// The host check runs even with no limits configured. It is the floor that
	// keeps an agent from starting the VM that sends the machine to swap.
	if force {
		return nil
	}
	avail := hostcheck.AvailableMB()
	if avail > 0 && v.RAM > avail {
		return fmt.Errorf("%w: %s needs %d MB and the host has %d MB available; `-y` starts it anyway",
			ErrLimit, v.Name, v.RAM, avail)
	}
	return nil
}

// runningQEMU names the running local VMs and sums their configured RAM.
func runningQEMU() ([]string, int) {
	vms, err := config.List()
	if err != nil {
		return nil, 0
	}
	var live []string
	total := 0
	for _, v := range vms {
		if v.Provider != "" {
			continue
		}
		state, err := StateOf(context.Background(), v)
		if err != nil || state != StateRunning {
			continue
		}
		live = append(live, fmt.Sprintf("%s %dMB", v.Name, v.RAM))
		total += v.RAM
	}
	sort.Strings(live)
	return live, total
}
