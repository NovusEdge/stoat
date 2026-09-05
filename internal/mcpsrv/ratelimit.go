package mcpsrv

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Limits are the token bucket sizes. The MCP spec makes rate limiting a
// server MUST. The numbers are generous enough that ordinary agent work
// never notices and tight enough that a runaway loop stops being the host's
// problem.
type Limits struct {
	ToolBurst int
	ToolRate  float64
	Burst     int
	Rate      float64
}

func DefaultLimits() Limits {
	return Limits{ToolBurst: 30, ToolRate: 0.5, Burst: 60, Rate: 2}
}

const sharedKey = ""

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter holds one bucket per tool name plus one every tool shares. The
// streamable HTTP transport serves concurrent requests, so the mutex is
// real, unlike the single-threaded Python original.
type limiter struct {
	lim Limits
	mu  sync.Mutex
	b   map[string]*bucket
}

func newLimiter(l Limits) *limiter {
	return &limiter{lim: l, b: map[string]*bucket{}}
}

// check consumes one token for tool and one shared token. Both buckets are
// read before either is charged: a hot tool that hits its own limit must not
// drain the shared bucket and starve every other tool.
func (l *limiter) check(tool string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	toolTokens, err := l.read(tool, l.lim.ToolBurst, l.lim.ToolRate, now, tool)
	if err != nil {
		return err
	}
	sharedTokens, err := l.read(sharedKey, l.lim.Burst, l.lim.Rate, now, "the server")
	if err != nil {
		return err
	}
	l.b[tool] = &bucket{tokens: toolTokens - 1, last: now}
	l.b[sharedKey] = &bucket{tokens: sharedTokens - 1, last: now}
	return nil
}

func (l *limiter) read(key string, capacity int, refill float64, now time.Time, subject string) (float64, error) {
	b, ok := l.b[key]
	if !ok {
		b = &bucket{tokens: float64(capacity), last: now}
	}
	tokens := math.Min(float64(capacity), b.tokens+now.Sub(b.last).Seconds()*refill)
	if tokens < 1 {
		wait := time.Duration((1 - tokens) / refill * float64(time.Second)).Round(time.Second)
		return 0, fmt.Errorf("rate limit reached for %s; retry in about %s", subject, wait)
	}
	return tokens, nil
}

// rateLimit is receiving middleware, so a refusal happens before the handler
// runs and before any argument is logged. Only tools/call is charged: a
// client listing tools or initializing is not doing work on a VM.
func (s *srv) rateLimit() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			name := "unknown"
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
				name = p.Name
			}
			if err := s.lim.check(name, time.Now()); err != nil {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				}, nil
			}
			return next(ctx, method, req)
		}
	}
}
