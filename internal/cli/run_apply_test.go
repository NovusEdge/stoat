package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/testutil"
)

// TestApplyJSONRedactsSecretsAcrossStreamChunks exercises the apply command's
// real log tail. The stored secret is split at the stream buffer boundary and
// the final line has no newline, so both the redactor and JSON line flusher
// must preserve the tail without exposing the secret.
func TestApplyJSONRedactsSecretsAcrossStreamChunks(t *testing.T) {
	dir := cliRoot(t)
	const (
		recipe  = "stream-redaction-caller"
		secret  = "abc"
		trailer = "last-line-without-newline"
	)
	recipeDir := filepath.Join(dir, "recipes", recipe)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 3
name = "stream-redaction-caller"
version = "1.0.0"
os = ["alpine"]
script = "install.sh"
run = "once"

[params.token]
type = "secret"
required = true
`
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	v := &config.VM{
		Name:    "stream-redaction-vm",
		OS:      "alpine",
		Mode:    "live",
		Backend: "apkovl",
		RAM:     1024,
		CPUs:    1,
		SSHPort: 2200,
		Recipes: []string{recipe},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSecrets(v.Dir, config.Secrets{recipe: {"token": secret}}); err != nil {
		t.Fatal(err)
	}

	hash, err := recipes.RecipeHash(recipe, v.OS, nil, []string{"token"})
	if err != nil {
		t.Fatal(err)
	}
	v.Applied = map[string]config.AppliedRecipe{
		recipe: {Version: "1.0.0", Hash: hash},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanApply(v.Name, core.ApplyOpts{})
	if err != nil {
		t.Fatalf("PlanApply: %v", err)
	}
	if len(plan) != 1 || plan[0].Action != "skip" || plan[0].Reason != "already applied" {
		t.Fatalf("plan = %+v, want one already-applied skip", plan)
	}

	// 32 KiB is the io.Copy buffer used by the apply log tail. Ending the
	// prefix four bytes before it puts the whole secret line at the end of
	// the first write, where the redactor's retained suffix is exercised.
	log := strings.Repeat("P", 32764) + secret + "\n" + trailer
	if err := os.WriteFile(v.ProvisionLogPath(), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := testutil.FakeRunning(t, v.Dir)
	defer stop()

	code, objs := runJSON(t, "apply", v.Name)
	if code != ExitOK {
		t.Fatalf("apply exit = %d, want %d: %v", code, ExitOK, objs)
	}
	raw, err := json.Marshal(objs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("apply output leaked stored secret %q: %s", secret, raw)
	}

	foundRedacted, foundTrailer := false, false
	for _, obj := range objs {
		if obj["type"] != "log" {
			continue
		}
		data, _ := obj["data"].(map[string]any)
		line, _ := data["line"].(string)
		if strings.Contains(line, "<redacted>") {
			foundRedacted = true
		}
		if strings.Contains(line, trailer) {
			foundTrailer = true
		}
	}
	if !foundRedacted {
		t.Errorf("apply log has no redaction marker: %v", objs)
	}
	if !foundTrailer {
		t.Errorf("apply log dropped trailing unterminated line %q: %v", trailer, objs)
	}
}

// The static source contains one intact secret whose first eight bytes are in
// io.Copy's first 32 KiB write and whose remaining bytes are in the next.
// This exercises the actual Main --json apply caller boundary while retaining
// the final unterminated tail assertion.
func TestApplyStreamRedactsSecretAcrossSourceWrites(t *testing.T) {
	dir := cliRoot(t)
	const (
		recipe = "stream-cross-write-caller"
		secret = "Zq7X9pL2-SECRET"
		tail   = "unterminated-tail-ALPHA"
	)
	recipeDir := filepath.Join(dir, "recipes", recipe)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 3
name = "stream-cross-write-caller"
os = ["alpine"]
script = "install.sh"

[params.token]
type = "secret"
required = true
`
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "stream-cross-write-vm", OS: "alpine", Mode: "live", Backend: "apkovl", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{recipe}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSecrets(v.Dir, config.Secrets{recipe: {"token": secret}}); err != nil {
		t.Fatal(err)
	}
	hash, err := recipes.RecipeHash(recipe, v.OS, nil, []string{"token"})
	if err != nil {
		t.Fatal(err)
	}
	v.Applied = map[string]config.AppliedRecipe{recipe: {Version: "1.0.0", Hash: hash}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanApply(v.Name, core.ApplyOpts{})
	if err != nil {
		t.Fatalf("PlanApply: %v", err)
	}
	if len(plan) != 1 || plan[0].Action != "skip" || plan[0].Reason != "already applied" {
		t.Fatalf("plan = %+v, want one already-applied skip", plan)
	}

	logPath := v.ProvisionLogPath()
	prefix := strings.Repeat("P", 32760)
	log := prefix + secret + "\n" + tail
	if !strings.HasPrefix(log[32760:], secret) || !strings.HasPrefix(log[32768:], secret[8:]) {
		t.Fatalf("fixture secret does not cross the 32 KiB source boundary: offset=%d", strings.Index(log, secret))
	}
	for _, fragment := range []string{secret[:8], secret[8:]} {
		if strings.Contains(tail, fragment) {
			t.Fatalf("fixture fragment %q overlaps preserved tail %q", fragment, tail)
		}
	}
	if err := os.WriteFile(logPath, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := testutil.FakeRunning(t, v.Dir)
	defer stop()

	code, objs := runJSON(t, "apply", v.Name)
	if code != ExitOK {
		t.Fatalf("apply exit = %d, want %d: %v", code, ExitOK, objs)
	}
	foundRedacted, foundTail := false, false
	for _, obj := range objs {
		if obj["type"] != "log" {
			continue
		}
		data, _ := obj["data"].(map[string]any)
		line, _ := data["line"].(string)
		for _, fragment := range []string{secret, secret[:8], secret[8:]} {
			if strings.Contains(line, fragment) {
				t.Fatalf("apply log event leaked secret fragment %q: %q", fragment, line)
			}
		}
		if strings.Contains(line, tail) {
			foundTail = true
		}
		if strings.Contains(line, "<redacted>") {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Fatalf("apply output has no redaction marker: %v", objs)
	}
	if !foundTail {
		t.Fatalf("apply output dropped unterminated tail %q: %v", tail, objs)
	}
}
