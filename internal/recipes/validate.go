package recipes

// ValidateTree is the public validation boundary for a cloned recipe tree.
// The implementation is supplied by the remote-recipes work; this zero-value
// stub keeps the test-first branch buildable until then.
func ValidateTree(string, string) error { return nil }

// CheckCollision is the public collision boundary for recipe add.
// The implementation is supplied by the remote-recipes work; this zero-value
// stub keeps the test-first branch buildable until then.
func CheckCollision(string, string) error { return nil }
