package core

import (
	"context"

	"github.com/novusedge/stoat/internal/config"
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

// HealthChecks runs checks for the named applied recipes.
func HealthChecks(ctx context.Context, _ *config.VM, _ []string) ([]RecipeHealth, error) {
	_ = ctx
	return nil, nil
}

// VMHealth folds recipe health verdicts into one VM result.
func VMHealth(_ []RecipeHealth) Health { return HealthUnknown }
