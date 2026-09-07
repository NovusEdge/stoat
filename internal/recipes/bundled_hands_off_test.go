package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handsOffExceptions names every bundled param a user must supply, because
// stoat has no way to know it. Adding a bundled recipe that needs
// configuration must add an entry here, on purpose.
var handsOffExceptions = map[string]map[string]bool{
	"tailscale": {"authkey": true},
}

// TestBundledRecipesApplyHandsOff resolves every bundled recipe's params
// against a representative VM for each guest it supports, with no
// user-supplied values. Resolution must succeed unless the param is listed
// in handsOffExceptions, which stands for a secret only the user holds.
func TestBundledRecipesApplyHandsOff(t *testing.T) {
	entries, err := os.ReadDir("bundled")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			m, err := ParseManifest(filepath.Join("bundled", name, "recipe.toml"))
			if err != nil {
				t.Fatal(err)
			}

			oses := m.OS
			if len(oses) == 0 {
				oses = []string{"ubuntu"}
			}
			for _, osName := range oses {
				secrets := map[string]string{}
				for paramName := range m.Params {
					if handsOffExceptions[name][paramName] {
						secrets[paramName] = "user-supplied"
					}
				}
				merged := WithVMDefaults(m, nil, "stoat")
				if _, err := Resolve(m, merged, secrets); err != nil {
					t.Errorf("%s on %s: Resolve with no user-supplied values (secrets from handsOffExceptions aside) failed: %v", name, osName, err)
				}
			}

			for paramName := range m.Params {
				if handsOffExceptions[name][paramName] {
					continue
				}
				if m.Params[paramName].Required && m.Params[paramName].DefaultFrom == "" && m.Params[paramName].Default == "" {
					t.Errorf("%s.%s: required with no default and no default_from; either supply one or add it to handsOffExceptions", name, paramName)
				}
			}
		})
	}
}

// TestBundledExceptionErrorNamesFixCommand pins that a genuinely
// user-supplied secret's unset error tells the user exactly what to run.
func TestBundledExceptionErrorNamesFixCommand(t *testing.T) {
	for recipeName, params := range handsOffExceptions {
		m, err := ParseManifest(filepath.Join("bundled", recipeName, "recipe.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for paramName := range params {
			_, err := Resolve(m, WithVMDefaults(m, nil, "stoat"), map[string]string{})
			if err == nil {
				t.Fatalf("%s.%s: expected an error with no secret supplied", recipeName, paramName)
			}
			want := "stoat update --secret " + recipeName + "." + paramName
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s.%s: error %q does not name %q", recipeName, paramName, err.Error(), want)
			}
		}
	}
}
