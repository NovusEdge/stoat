package core

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
