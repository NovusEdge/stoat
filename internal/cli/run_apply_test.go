package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// The source itself can be extended between two streamFile copies. A
// distinctive secret fragment must not be emitted from the first copy before
// the rest arrives, and an unterminated tail must survive the final flush.
func TestApplyStreamRedactsSecretAcrossSourceWrites(t *testing.T) {
	dir := cliRoot(t)
	const (
		recipe = "stream-cross-write-caller"
		secret = "cross-write-secret"
		tail   = "unterminated-cross-write-tail"
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

	logPath := v.ProvisionLogPath()
	first := strings.Repeat("P", 64) + secret[:8] + strings.Repeat("Q", len(secret)+64)
	if err := os.WriteFile(logPath, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	ready := make(chan struct{})
	sink := &firstWriteWriter{dst: &got, ready: ready}
	redactor, err := newSecretRedactor(v.Dir, sink)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan error, 1)
	result := make(chan error, 1)
	go func() { result <- streamFile(logPath, redactor, stop) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("streamFile did not copy the initial source chunk")
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, secret[8:]+"\n"+tail); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	stop <- nil
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := redactor.Flush(); err != nil {
		t.Fatal(err)
	}
	output := got.String()
	for _, fragment := range []string{secret, secret[:8], secret[8:]} {
		if strings.Contains(output, fragment) {
			t.Fatalf("stream output leaked secret fragment %q: %q", fragment, output)
		}
	}
	if !strings.Contains(output, "<redacted>") {
		t.Fatalf("stream output has no redaction marker: %q", output)
	}
	if !strings.Contains(output, tail) {
		t.Fatalf("stream output dropped unterminated tail %q: %q", tail, output)
	}
}

type firstWriteWriter struct {
	dst   io.Writer
	ready chan struct{}
	once  sync.Once
}

func (w *firstWriteWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.once.Do(func() { close(w.ready) })
	return n, err
}
