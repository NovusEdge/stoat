package mcpsrv

import (
	"strings"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	start := time.Unix(0, 0)

	t.Run("allows_up_to_capacity", func(t *testing.T) {
		l := newLimiter(Limits{ToolBurst: 3, ToolRate: 0.5, Burst: 100, Rate: 2})
		for i := range 3 {
			if err := l.check("list_vms", start); err != nil {
				t.Fatalf("call %d refused: %v", i, err)
			}
		}
		err := l.check("list_vms", start)
		if err == nil || !strings.Contains(err.Error(), "rate limit") {
			t.Fatalf("call 4 error = %v, want a rate limit refusal", err)
		}
	})

	t.Run("independent_per_tool", func(t *testing.T) {
		l := newLimiter(Limits{ToolBurst: 1, ToolRate: 0.5, Burst: 100, Rate: 2})
		if err := l.check("list_vms", start); err != nil {
			t.Fatal(err)
		}
		if err := l.check("doctor", start); err != nil {
			t.Fatalf("a second tool was refused: %v", err)
		}
	})

	t.Run("refills", func(t *testing.T) {
		l := newLimiter(Limits{ToolBurst: 1, ToolRate: 0.5, Burst: 100, Rate: 2})
		if err := l.check("doctor", start); err != nil {
			t.Fatal(err)
		}
		if err := l.check("doctor", start); err == nil {
			t.Fatal("second call not refused")
		}
		if err := l.check("doctor", start.Add(2*time.Second)); err != nil {
			t.Fatalf("no refill after 2s at 0.5/s: %v", err)
		}
	})

	t.Run("shared_bucket", func(t *testing.T) {
		// Per-tool buckets alone let a caller burst ToolBurst times against
		// each of ~40 tools. The shared bucket is what bounds the server.
		l := newLimiter(Limits{ToolBurst: 100, ToolRate: 0.5, Burst: 2, Rate: 2})
		if err := l.check("a", start); err != nil {
			t.Fatal(err)
		}
		if err := l.check("b", start); err != nil {
			t.Fatal(err)
		}
		if err := l.check("c", start); err == nil {
			t.Fatal("the shared bucket did not bound a third tool")
		}
	})

	t.Run("refusal_charges_nothing", func(t *testing.T) {
		l := newLimiter(Limits{ToolBurst: 1, ToolRate: 0.5, Burst: 10, Rate: 2})
		if err := l.check("hot", start); err != nil {
			t.Fatal(err)
		}
		for range 5 {
			if err := l.check("hot", start); err == nil {
				t.Fatal("hot tool not refused")
			}
		}
		// Nine shared tokens must be left: one spent by the call that
		// succeeded, none by the five refusals.
		for i := range 9 {
			if err := l.check("cold", start); err != nil {
				t.Fatalf("cold call %d refused, so a refusal spent a shared token: %v", i, err)
			}
		}
	})
}
