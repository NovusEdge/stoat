package wire

import (
	"context"
	"errors"
	"slices"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/qemu"
)

// Code is a stable, machine-readable error code. Codes are only ever ADDED:
// never renamed, never repurposed, never removed (§2's compatibility
// promise). A consumer MUST treat an unrecognized code as a generic failure.
type Code string

// Stable snake_case error codes (§2), one per typed error in internal/core
// plus the three the CLI layer owns (usage, confirmation_required,
// internal). Codes are only ever ADDED: never renamed, never repurposed,
// never removed (§2's compatibility promise). A consumer MUST treat an
// unrecognized code as a generic failure, never crash on it.
const (
	CodeNotFound             Code = "not_found"
	CodeBroken               Code = "broken"
	CodeNameTaken            Code = "name_taken"
	CodeInvalidSpec          Code = "invalid_spec"
	CodeImageNotDownloaded   Code = "image_not_downloaded"
	CodeRecipeNotApplicable  Code = "recipe_not_applicable"
	CodeNotRunning           Code = "not_running"
	CodeAlreadyRunning       Code = "already_running"
	CodeNoDisk               Code = "no_disk"
	CodeImmutableField       Code = "immutable_field"
	CodeDiskShrink           Code = "disk_shrink"
	CodeCannotReach          Code = "cannot_reach"
	CodeUnknownLog           Code = "unknown_log"
	CodeTimeout              Code = "timeout"
	CodeCanceled             Code = "canceled"
	CodeUsage                Code = "usage"
	CodeConfirmationRequired Code = "confirmation_required"
	CodeInternal             Code = "internal"

	CodeQemuMissing        Code = "qemu_missing"
	CodeKVMUnusable        Code = "kvm_unusable"
	CodeQemuStartFailed    Code = "qemu_start_failed"
	CodeMonitorUnreachable Code = "monitor_unreachable"
	CodeMonitorRejected    Code = "monitor_rejected"
	CodeNoConsolePassword  Code = "no_console_password"
	CodeShareInvalid       Code = "share_invalid"
	CodeNoXattr            Code = "no_xattr"

	CodeDownloadFailed   Code = "download_failed"
	CodeDownloadStalled  Code = "download_stalled"
	CodeChecksumMismatch Code = "checksum_mismatch"
	CodeNoSuchImage      Code = "no_such_image"
)

// Codes returns every declared code, sorted. Built from the same string
// constants codeTable and ErrorInfo.Code use, so a code added to one and
// forgotten here fails TestCodesCoversTheTable.
func Codes() []Code {
	out := []Code{
		CodeNotFound, CodeBroken, CodeNameTaken, CodeInvalidSpec,
		CodeImageNotDownloaded, CodeRecipeNotApplicable, CodeNotRunning,
		CodeAlreadyRunning, CodeNoDisk, CodeImmutableField, CodeDiskShrink,
		CodeCannotReach, CodeUnknownLog, CodeTimeout, CodeCanceled,
		CodeUsage, CodeConfirmationRequired, CodeInternal,
		CodeQemuMissing, CodeKVMUnusable, CodeQemuStartFailed,
		CodeMonitorUnreachable, CodeMonitorRejected, CodeNoConsolePassword,
		CodeShareInvalid, CodeNoXattr,
		CodeDownloadFailed, CodeDownloadStalled, CodeChecksumMismatch,
		CodeNoSuchImage,
	}
	slices.Sort(out)
	return out
}

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
	code Code
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
	{CodeQemuMissing, qemu.ErrBinaryMissing},
	{CodeKVMUnusable, qemu.ErrKVMUnusable},
	{CodeQemuStartFailed, qemu.ErrStartFailed},
	{CodeMonitorUnreachable, qemu.ErrMonitorUnreachable},
	{CodeMonitorRejected, qemu.ErrMonitorRejected},
	{CodeNoConsolePassword, qemu.ErrNoConsolePassword},
	{CodeShareInvalid, qemu.ErrShareInvalid},
	{CodeNoXattr, qemu.ErrNoXattr},
	{CodeAlreadyRunning, qemu.ErrAlreadyRunning},
	{CodeDownloadFailed, iso.ErrDownloadFailed},
	{CodeDownloadStalled, iso.ErrDownloadStalled},
	{CodeChecksumMismatch, iso.ErrChecksumMismatch},
	{CodeNoSuchImage, iso.ErrNoSuchImage},
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
