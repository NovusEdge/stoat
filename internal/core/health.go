package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/sshx"
)

// Health is a recipe's health-check result. The recipe contract writes the
// checks that report it; this declares the values they may report.
type Health string

const (
	HealthOK      Health = "ok"
	HealthFailed  Health = "failed"
	HealthUnknown Health = "unknown"
)

// Healths returns every declared health value.
func Healths() []Health { return []Health{HealthOK, HealthFailed, HealthUnknown} }

// RecipeHealth is one recipe health verdict.
type RecipeHealth struct {
	Name   string
	Status Health
	Detail string
}

// healthChecksForVM runs checks for the named recipes in order and records
// each verdict on an existing applied entry. It does not save v.
func healthChecksForVM(ctx context.Context, v *config.VM, names []string) ([]RecipeHealth, error) {
	out := make([]RecipeHealth, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, err
		}
		verdict := RecipeHealth{Name: name, Status: HealthUnknown}
		if ok && m.Health.Check != "" {
			text, runErr := sshx.RunCheck(ctx, v, m.Health.Check, m.Health.Duration())
			if runErr != nil {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				stored, loadErr := config.LoadSecrets(v.Dir)
				if loadErr != nil {
					return nil, loadErr
				}
				verdict.Status = HealthFailed
				detail := redactCloudSecrets(text, stored[name])
				verdict.Detail = fmt.Sprintf("health check failed after %s: %s", m.Health.Duration(), lastLine(detail))
			} else {
				verdict.Status = HealthOK
			}
		}
		if a, recorded := v.Applied[name]; recorded {
			a.Health = string(verdict.Status)
			v.Applied[name] = a
		}
		out = append(out, verdict)
	}
	return out, nil
}

// HealthChecks checks the named VM's applied recipes in configured order.
// The public operation is implemented by the recipe-health follow-up; the
// loaded-VM runner above remains the Apply-local mechanical seam.
func HealthChecks(ctx context.Context, name string) ([]RecipeHealth, error) {
	return nil, nil
}

// VMHealth folds verdicts into one VM result.
func VMHealth(rs []RecipeHealth) Health {
	status := HealthUnknown
	for _, result := range rs {
		if result.Status == HealthFailed {
			return HealthFailed
		}
		if result.Status == HealthOK {
			status = HealthOK
		}
	}
	return status
}

// HealthTimeout is the longest declared health check among applied recipes.
func HealthTimeout(v *config.VM) time.Duration {
	longest := recipes.DefaultHealthTimeout
	for name := range v.Applied {
		if m, ok, _ := recipes.ManifestFor(name); ok && m.Health.Duration() > longest {
			longest = m.Health.Duration()
		}
	}
	return longest
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
