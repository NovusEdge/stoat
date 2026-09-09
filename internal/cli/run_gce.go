package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/provider"
)

func runGCE(a *Args, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "extend":
		return runGCEExtend(a, stdout, stderr)
	default:
		return a.failUsage(stdout, stderr, "unknown gce subcommand "+a.Sub)
	}
}

// runGCEExtend moves a running gce instance's soft deadline forward. It
// loads config.VM directly, alongside core.Get, because core.Get's Provider
// interface carries no Extend: that would put a gce-only operation on every
// provider's contract for the sake of one command.
func runGCEExtend(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if v.State == core.StateBroken {
		return a.failMsg(stdout, stderr, core.ErrBroken, v.Error)
	}
	cfg, err := config.Load(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	p, err := provider.For(cfg)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	ext, ok := p.(provider.Extender)
	if !ok {
		return a.failMsg(stdout, stderr, core.ErrInvalidSpec, a.VM+" is not a gce VM")
	}
	soft, err := ext.Extend(context.Background(), cfg, a.Duration)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm":            a.VM,
			"soft_deadline": soft.UTC().Format(time.RFC3339),
			"hard_deadline": rfc3339OrEmpty(v.HardDeadline),
		})
	}
	fmt.Fprintf(stdout, "%s: soft deadline now %s (in %s)\n", a.VM, soft.UTC().Format(time.RFC3339), formatDuration(time.Until(soft)))
	return ExitOK
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
