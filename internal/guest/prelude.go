package guest

// Prelude renders the verb definitions and STOAT_* variables for runtime
// ("sh" or "python3"). Stub: Task 5 fills this in.
func Prelude(o OS, runtime string) string { return "" }

// WithPrelude inserts prelude after a leading "#!" line, or in front of the
// body when there is none. Stub: Task 5 fills this in.
func WithPrelude(body, prelude string) string { return "" }

// ShQuote wraps s in single quotes for POSIX sh. Stub: Task 5 fills this in.
func ShQuote(s string) string { return "" }
