package mcpsrv

// Level is an agent_access level. Task 8 adds requireAccess and its table;
// this chunk only needs the type for toolTable's Access field.
type Level int

const (
	LevelNone Level = iota
	LevelObserve
	LevelManage
	LevelExec
)
