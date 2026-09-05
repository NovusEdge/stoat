package mcpsrv

import "time"

// execTimeout is not implemented yet. Task 12 clamps seconds to
// [1, maxExecSecs], defaulting to 60 when seconds is 0.
func execTimeout(seconds int) time.Duration {
	return 0
}
