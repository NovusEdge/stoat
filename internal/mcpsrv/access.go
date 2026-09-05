package mcpsrv

import "fmt"

// Level is an agent_access level. Task 8 adds requireAccess and its table;
// this chunk only needs the type for toolTable's Access field.
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

// ParseLevel is not implemented yet. Task 8 validates s against levelNames.
func ParseLevel(s string) (Level, error) {
	return LevelNone, fmt.Errorf("agent_access %q: not implemented", s)
}

// requireAccess is not implemented yet. Task 8 gates every guest-touching
// tool at the level toolTable declares for it.
func requireAccess(vm string, need Level) error {
	return fmt.Errorf("requireAccess(%q, %s): not implemented", vm, need)
}
