package recipes

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// OutputDir is the guest directory used for per-recipe output files.
const OutputDir = "/tmp/.stoat-out"

// ErrParamUnset is Resolve's sentinel for a required param or secret with no
// value: internal/cli/wire maps it to invalid_spec, the same row as
// ErrInvalidTree.
var ErrParamUnset = errors.New("required parameter is unset")

// Resolve merges manifest defaults with values stored for one recipe. Secret
// values come from the separate secrets map and never become VM parameters.
func Resolve(m Manifest, set map[string]string, secrets map[string]string) (map[string]string, error) {
	params := m.SortedParams()
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	for name := range set {
		if _, ok := m.Params[name]; !ok {
			return nil, fmt.Errorf("%s: no param %q; has %s", m.Name, name, strings.Join(names, ", "))
		}
	}

	out := make(map[string]string, len(params))
	for _, p := range params {
		value, present := "", false
		if p.Type == "secret" {
			value, present = secrets[p.Name]
			present = present && value != ""
		} else if value, present = set[p.Name]; !present {
			value = p.Default
			present = value != ""
		}
		if !present {
			if p.Required {
				return nil, requiredUnset(m.Name, p)
			}
			out[p.Name] = ""
			continue
		}
		if err := Validate(m, p.Name, value); err != nil {
			return nil, err
		}
		out[p.Name] = value
	}
	return out, nil
}

func requiredUnset(recipe string, p Param) error {
	if p.Type == "secret" {
		return paramUnset{fmt.Errorf("%s.%s: required secret is unset; run stoat update --secret %s.%s", recipe, p.Name, recipe, p.Name)}
	}
	return paramUnset{fmt.Errorf("%s.%s: required param is unset; run stoat update --set %s.%s=VALUE", recipe, p.Name, recipe, p.Name)}
}

// paramUnset carries ErrParamUnset without a "%w: " prefix on its message: a
// secret's failure text must read "required secret is unset", not "required
// parameter is unset: required secret is unset".
type paramUnset struct{ error }

func (paramUnset) Unwrap() error { return ErrParamUnset }

// Validate checks one declared parameter value against its manifest type.
func Validate(m Manifest, name, value string) error {
	p, ok := m.Params[name]
	if !ok {
		names := make([]string, 0, len(m.Params))
		for _, declared := range m.SortedParams() {
			names = append(names, declared.Name)
		}
		return fmt.Errorf("%s: no param %q; has %s", m.Name, name, strings.Join(names, ", "))
	}
	switch p.Type {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("%s.%s: %q is not an integer", m.Name, name, value)
		}
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("%s.%s: %q is not true or false", m.Name, name, value)
		}
	case "enum":
		if !containsString(p.Values, value) {
			return fmt.Errorf("%s.%s: %q is not one of %s", m.Name, name, value, strings.Join(p.Values, ", "))
		}
	}
	return nil
}

// RecipeHash covers the script, resolved non-secret parameters, and names of
// set secret parameters. Secret values never enter this digest.
func RecipeHash(name, osName string, params map[string]string, secretNames []string) (string, error) {
	body, err := ScriptBody(name, osName)
	if err != nil {
		return "", err
	}
	if len(params) == 0 && len(secretNames) == 0 {
		return sum([]byte(body)), nil
	}
	var b strings.Builder
	b.WriteString(body)
	paramNames := make([]string, 0, len(params))
	for name := range params {
		paramNames = append(paramNames, name)
	}
	sort.Strings(paramNames)
	for _, name := range paramNames {
		fmt.Fprintf(&b, "\n\x00param %s=%s", name, params[name])
	}
	secretNames = append([]string(nil), secretNames...)
	sort.Strings(secretNames)
	for _, name := range secretNames {
		fmt.Fprintf(&b, "\n\x00secret %s", name)
	}
	return sum([]byte(b.String())), nil
}

// Env renders STOAT_RECIPE and sorted parameter variables for a guest shell.
func Env(recipe string, params map[string]string) []string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]string, 0, len(names)+1)
	env = append(env, "STOAT_RECIPE="+recipe)
	for _, name := range names {
		env = append(env, "STOAT_PARAM_"+strings.ToUpper(name)+"="+params[name])
	}
	return env
}
