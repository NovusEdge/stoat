package mcpsrv

import (
	"fmt"
	"time"
)

// Limits are the token bucket sizes. The MCP spec makes rate limiting a
// server MUST.
type Limits struct {
	ToolBurst int
	ToolRate  float64
	Burst     int
	Rate      float64
}

func DefaultLimits() Limits {
	return Limits{}
}

// limiter is a stub; ratelimit_test.go pins its real behaviour.
type limiter struct{}

func newLimiter(l Limits) *limiter {
	return &limiter{}
}

func (l *limiter) check(tool string, now time.Time) error {
	return fmt.Errorf("not implemented")
}
