package wire

// The status values a DTO carries, so no constructor writes a raw literal.
// They are plain strings, not a named type: wire.VM.State is a string field
// and a consumer reads the JSON, not the Go type.
const (
	StateStopped = "stopped"
	StateRunning = "running"
	StateBroken  = "broken"

	HealthOK      = "ok"
	HealthFailed  = "failed"
	HealthUnknown = "unknown"
)
