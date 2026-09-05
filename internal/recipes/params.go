package recipes

// OutputDir is the guest directory used for per-recipe output files.
const OutputDir = "/tmp/.stoat-out"

// Resolve is the parameter-resolution boundary. The implementation is added
// after the RED contract tests in this chunk.
func Resolve(Manifest, map[string]string, map[string]string) (map[string]string, error) {
	return nil, nil
}

// Validate checks one declared parameter value.
func Validate(Manifest, string, string) error { return nil }

// RecipeHash computes a script-and-parameter hash.
func RecipeHash(string, string, map[string]string, []string) (string, error) {
	return "", nil
}

// Env renders the guest parameter environment.
func Env(string, map[string]string) []string { return nil }
