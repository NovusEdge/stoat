package sshx

import "github.com/novusedge/stoat/internal/recipes"

// OutputDir is the guest directory used for per-recipe output files.
const OutputDir = recipes.OutputDir

// ParseOutputs parses a recipe's output file. The implementation follows the
// RED tests in this chunk.
func ParseOutputs(map[string]string, string) (map[string]string, []string) {
	return nil, nil
}
