package wire

import (
	"context"
	"errors"

	"github.com/novusedge/stoat/internal/core"
)

// Stable snake_case error codes (§2), one per typed error in internal/core
// plus the three the CLI layer owns (usage, confirmation_required,
// internal). Codes are only ever ADDED: never renamed, never repurposed,
// never removed (§2's compatibility promise). A consumer MUST treat an
// unrecognized code as a generic failure, never crash on it.
const (
	CodeNotFound             = "not_found"
	CodeBroken               = "broken"
	CodeNameTaken            = "name_taken"
	CodeInvalidSpec          = "invalid_spec"
	CodeImageNotDownloaded   = "image_not_downloaded"
	CodeRecipeNotApplicable  = "recipe_not_applicable"
	CodeNotRunning           = "not_running"
	CodeAlreadyRunning       = "already_running"
	CodeNoDisk               = "no_disk"
	CodeImmutableField       = "immutable_field"
	CodeDiskShrink           = "disk_shrink"
	CodeCannotReach          = "cannot_reach"
	CodeUnknownLog           = "unknown_log"
	CodeTimeout              = "timeout"
	CodeCanceled             = "canceled"
	CodeUsage                = "usage"
	CodeConfirmationRequired = "confirmation_required"
	CodeInternal             = "internal"
)

// Code is a stable, machine-readable error code. Codes are only ever ADDED:
// never renamed, never repurposed, never removed (§2's compatibility
// promise). A consumer MUST treat an unrecognized code as a generic failure.
type Code string

// Codes returns every declared code, sorted.
func Codes() []Code { return []Code{"STUB"} }

// ErrConfirmationRequired is confirmation_required's sentinel (§2: "new,
// CLI-only"). core has no equivalent; --json never prompts (§1), so `rm`
// without -y wraps this instead of reading stdin. A future cli.go caller
// wraps it the same way core wraps its own sentinels:
// fmt.Errorf("%w: %s", wire.ErrConfirmationRequired, name).
var ErrConfirmationRequired = errors.New("confirmation required; pass -y under --json")

// codeTable is the ordered errors.Is chain (§2): first match wins, checked
// by MapError. It holds one row per typed error in internal/core, plus this
// package's own ErrConfirmationRequired.
//
// Order does not currently break a tie: no core error wraps two of these
// sentinels at once. update.go's disk-grow-while-running path wraps
// ErrAlreadyRunning directly, never ErrDiskShrink, so there is nothing to
// arbitrate there. Rows keep the design doc's order in case that changes.
var codeTable = []struct {
	code string
	err  error
}{
	{CodeNotFound, core.ErrNotFound},
	{CodeBroken, core.ErrBroken},
	{CodeNameTaken, core.ErrNameTaken},
	{CodeInvalidSpec, core.ErrInvalidSpec},
	{CodeImageNotDownloaded, core.ErrImageNotDownloaded},
	{CodeRecipeNotApplicable, core.ErrRecipeNotApplicable},
	{CodeNotRunning, core.ErrNotRunning},
	{CodeAlreadyRunning, core.ErrAlreadyRunning},
	{CodeNoDisk, core.ErrNoDisk},
	{CodeImmutableField, core.ErrImmutableField},
	{CodeDiskShrink, core.ErrDiskShrink},
	{CodeCannotReach, core.ErrCannotReach},
	{CodeUnknownLog, core.ErrUnknownWhich},
	{CodeTimeout, context.DeadlineExceeded},
	{CodeCanceled, context.Canceled},
	{CodeConfirmationRequired, ErrConfirmationRequired},
}

// MapError converts a core (or context) error into an ErrorInfo, walking
// codeTable in order and taking the first errors.Is match. An error that
// matches nothing in the table is CodeInternal with the raw Go error
// string: the escape hatch (§2) that guarantees a well-formed envelope even
// for a failure nobody anticipated. Returns nil for a nil err, so a caller
// can pass a possibly-nil error straight through without an extra check.
//
// "usage" is deliberately NOT in codeTable: cli.usageError carries no
// sentinel to match against (every Parse failure is a distinct message, not
// a wrapped value), so a usage failure is built directly with UsageError
// instead of routed through here.
func MapError(err error) *ErrorInfo {
	if err == nil {
		return nil
	}
	for _, e := range codeTable {
		if errors.Is(err, e.err) {
			return &ErrorInfo{Code: e.code, Message: err.Error()}
		}
	}
	return &ErrorInfo{Code: CodeInternal, Message: err.Error()}
}

// UsageError builds the "usage" envelope (§2) for a cli.usageError message,
// exit code 2. Not routed through MapError; see its doc comment.
func UsageError(message string) *ErrorInfo {
	return &ErrorInfo{Code: CodeUsage, Message: message}
}

// InternalError builds a bare "internal" envelope (§2) for a failure with no
// Go error to map, such as Main's panic recovery: recover() returns any,
// not error, so there is nothing for MapError to call errors.Is against.
func InternalError(message string) *ErrorInfo {
	return &ErrorInfo{Code: CodeInternal, Message: message}
}
