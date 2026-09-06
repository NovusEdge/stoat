package mcpsrv

import (
	"fmt"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
)

// Level is an agent_access level. Each level includes the ones below it.
//
//	none    host side only: status, start, stop, snapshot, restore, logs,
//	        forward, update
//	observe read_file, list_dir, stat, ps, svc_status, tail_log
//	manage  write_file, copy_to, copy_from, pkg_install, svc, useradd,
//	        apply_recipes
//	exec    exec, exec_bg, job_status, job_output, job_kill, list_jobs
type Level int

const (
	LevelNone Level = iota
	LevelObserve
	LevelManage
	LevelExec
)

// levelNames is the string form of a Level: vm.toml's agent_access key, the
// CLI's --agent-access flag, and every refusal message all read through it.
var levelNames = [...]string{"none", "observe", "manage", "exec"}

func (l Level) String() string {
	if l < LevelNone || int(l) >= len(levelNames) {
		return "invalid"
	}
	return levelNames[l]
}

// rank orders Level for comparison. Level's own iota order is already that
// rank, since each level includes every one below it.
func (l Level) rank() int { return int(l) }

// ParseLevel validates s against the four declared level names.
func ParseLevel(s string) (Level, error) {
	for i, name := range levelNames {
		if s == name {
			return Level(i), nil
		}
	}
	return 0, fmt.Errorf("invalid agent_access %q: one of none, observe, manage, exec", s)
}

// currentLevel reads vm's agent_access straight off vm.toml.
func currentLevel(vm string) (Level, error) {
	name, err := checkVMName(vm)
	if err != nil {
		return 0, err
	}
	v, err := config.Load(name)
	if err != nil {
		return 0, err
	}
	return ParseLevel(v.AgentAccess)
}

// requireAccess gates every guest-touching tool. core.Exec does not enforce
// it, because core is a library the CLI and TUI also call and a blanket
// refusal there would be the wrong layer. The refusal names both levels, so
// an agent knows what to ask a person for.
func requireAccess(vm string, need Level) error {
	have, err := currentLevel(vm)
	if err != nil {
		return err
	}
	if have.rank() < need.rank() {
		return wire.WithSentinel(fmt.Errorf("vm %q has agent_access = %s; needs %s", vm, have, need), wire.ErrAccessDenied)
	}
	return nil
}
