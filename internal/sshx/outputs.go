package sshx

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

// OutputDir is the guest directory used for per-recipe output files.
const OutputDir = recipes.OutputDir

// ParseOutputs parses name=value lines and keeps undeclared names so a recipe
// result is not lost when its manifest was incomplete. Empty values count.
func ParseOutputs(declared map[string]string, body string) (map[string]string, []string) {
	values := map[string]string{}
	var undeclared []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			continue
		}
		values[name] = value
		if _, ok := declared[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	return values, undeclared
}

func collectOutputs(ctx context.Context, v *config.VM, name string, m recipes.Manifest, secrets []string, log io.Writer) error {
	path := OutputDir + "/" + name
	quoted := shellPath(path)
	script := fmt.Sprintf("cat %s 2>/dev/null; rm -f %s", quoted, quoted)
	out, err := exec.CommandContext(ctx, "ssh", Args(v, escalate(v, []string{"sh", "-c", script})...)...).Output()
	if err != nil {
		return err
	}
	values, undeclared := ParseOutputs(m.Outputs, redactString(string(out), secrets))
	for _, output := range undeclared {
		fmt.Fprintf(log, "%s: output %q is not declared\n", m.Name, output)
	}
	if v.Applied == nil {
		v.Applied = map[string]config.AppliedRecipe{}
	}
	a := v.Applied[name]
	a.Outputs = values
	v.Applied[name] = a
	return nil
}
