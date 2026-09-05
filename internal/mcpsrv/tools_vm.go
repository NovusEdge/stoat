package mcpsrv

import "time"

// updateIn is the input for the update tool. Task 7's implementation adds
// the remaining fields (CPUs, SSHPort, Disk, Recipes, Params, Secrets,
// AgentAccess); RAMMB is the only one this chunk's tests exercise.
type updateIn struct {
	VM    string
	RAMMB int
}

// waitTimeout is not implemented yet. Task 7 clamps seconds to
// [1, maxWaitSecs] and returns the equivalent time.Duration.
func waitTimeout(seconds int) time.Duration {
	return 0
}

// patchFromUpdate is not implemented yet. Task 7 builds the patch map from
// in's non-zero fields and runs it through stripForbidden.
func patchFromUpdate(in updateIn) map[string]any {
	return map[string]any{}
}
