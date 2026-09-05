package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

// parseParamFlags turns parameter flag values into edits without consulting
// the environment or prompting. Secret resolution belongs to Main, where the
// caller supplies stdin and stderr.
func parseParamFlags(set, unset, secret []string) ([]ParamEdit, error) {
	var edits []ParamEdit
	for _, raw := range set {
		target, value, ok := strings.Cut(raw, "=")
		recipe, param, valid := splitParamTarget(target)
		if !valid || !ok {
			return nil, usageError(fmt.Sprintf("--set %s: want <recipe>.<param>=<value>", raw))
		}
		edits = append(edits, ParamEdit{Recipe: recipe, Param: param, Value: value})
	}
	for _, raw := range unset {
		recipe, param, valid := splitParamTarget(raw)
		if !valid || strings.Contains(raw, "=") {
			return nil, usageError(fmt.Sprintf("--unset %s: want <recipe>.<param>", raw))
		}
		edits = append(edits, ParamEdit{Recipe: recipe, Param: param, Unset: true})
	}
	for _, raw := range secret {
		recipe, param, valid := splitParamTarget(raw)
		if !valid || strings.Contains(raw, "=") {
			return nil, usageError(fmt.Sprintf("--secret %s: want <recipe>.<param>", raw))
		}
		edits = append(edits, ParamEdit{Recipe: recipe, Param: param, Secret: true})
	}
	return edits, nil
}

func splitParamTarget(target string) (recipe, param string, ok bool) {
	recipe, param, ok = strings.Cut(target, ".")
	return recipe, param, ok && recipe != "" && param != ""
}

// resolveParamEdits fills secret values at the run boundary. A real terminal
// permits a prompt; JSON and non-terminal callers must provide an environment
// value so a command never blocks waiting for input.
func resolveParamEdits(edits []ParamEdit, stdin io.Reader, stderr io.Writer, tty bool) ([]ParamEdit, error) {
	resolved := append([]ParamEdit(nil), edits...)
	for i := range resolved {
		if !resolved[i].Secret {
			continue
		}
		e := &resolved[i]
		envName := secretEnvName(e.Recipe, e.Param)
		if value, ok := os.LookupEnv(envName); ok && value != "" {
			e.Value = value
			continue
		}
		if !tty {
			return nil, fmt.Errorf("--secret %s.%s: set %s or run without --json", e.Recipe, e.Param, envName)
		}
		fmt.Fprintf(stderr, "%s.%s: ", e.Recipe, e.Param)
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("--secret %s.%s: %w", e.Recipe, e.Param, err)
		}
		e.Value = strings.TrimRight(line, "\r\n")
		if e.Value == "" {
			return nil, fmt.Errorf("--secret %s.%s: no value given", e.Recipe, e.Param)
		}
	}
	return resolved, nil
}

func secretEnvName(recipe, param string) string {
	return "STOAT_SECRET_" + strings.ToUpper(recipe) + "_" + strings.ToUpper(param)
}

// streamIsTTY identifies the process terminal without requiring callers to
// pass an os.File. Test callers use an ordinary io.Reader and therefore take
// the non-interactive environment path.
func streamIsTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// paramMaps splits parsed edits into the storage shapes core accepts.
func paramMaps(edits []ParamEdit) (set map[string]map[string]string, unset map[string][]string, secrets config.Secrets) {
	set = map[string]map[string]string{}
	unset = map[string][]string{}
	secrets = config.Secrets{}
	for _, edit := range edits {
		switch {
		case edit.Unset:
			unset[edit.Recipe] = append(unset[edit.Recipe], edit.Param)
		case edit.Secret:
			if secrets[edit.Recipe] == nil {
				secrets[edit.Recipe] = map[string]string{}
			}
			secrets[edit.Recipe][edit.Param] = edit.Value
		default:
			if set[edit.Recipe] == nil {
				set[edit.Recipe] = map[string]string{}
			}
			set[edit.Recipe][edit.Param] = edit.Value
		}
	}
	return set, unset, secrets
}
