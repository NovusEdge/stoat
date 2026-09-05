package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/testutil"
)

func cliRecipeRepo(t *testing.T, name, script string) string {
	t.Helper()
	src := testutil.GitRepo(t, map[string]string{
		"recipe.toml": fmt.Sprintf("schema = 3\nname = %q\ndescription = %q\nos = [\"alpine\"]\nrequires = [\"git\"]\nscript = \"install.sh\"\n\n[params.channel]\ntype = \"enum\"\nvalues = [\"stable\", \"test\"]\ndefault = \"stable\"\n", name, name+" recipe"),
		"install.sh":  script,
	})
	dst := filepath.Join(filepath.Dir(src), name+".git")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func cliIndex(t *testing.T, entries string) string {
	t.Helper()
	index := testutil.GitRepo(t, map[string]string{"index.toml": "schema = 1\n\n" + entries})
	t.Setenv("STOAT_INDEX", index)
	return index
}

func writeCLIFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeAddAndListReportScopeAndCommit(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\nset -e\necho v1\n")

	if code, _ := runJSON(t, "recipe", "add", src, "-y", "--global"); code != ExitOK {
		t.Fatalf("add exit = %d", code)
	}
	code, objs := runJSON(t, "recipe", "list")
	if code != ExitOK {
		t.Fatalf("list exit = %d", code)
	}
	data := dataOf(t, objs)
	entries, ok := data["recipes"].([]any)
	if !ok {
		t.Fatalf("recipes = %#v, want an array of named entries", data["recipes"])
	}
	var found map[string]any
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m["name"] == "demo" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("demo missing from %v", entries)
	}
	if found["scope"] != "global" {
		t.Errorf("scope = %v, want global", found["scope"])
	}
	if s, _ := found["commit"].(string); len(s) != 7 {
		t.Errorf("commit = %v, want a 7-character sha", found["commit"])
	}
}

func TestRecipeLockPersistsResolvedPinAndSyncCreatesCache(t *testing.T) {
	cliRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho v1\n")
	writeCLIFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src))

	code, objs := runJSON(t, "recipe", "lock")
	if code != ExitOK {
		t.Fatalf("lock exit = %d: %v", code, objs)
	}
	rows, ok := dataOf(t, objs)["recipes"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("lock data.recipes = %#v, want one named row", dataOf(t, objs)["recipes"])
	}
	row, _ := rows[0].(map[string]any)
	if row["name"] != "demo" {
		t.Errorf("lock row name = %v, want demo", row["name"])
	}
	scope, err := recipes.ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Recipes["demo"]
	if !ok || entry.Commit == "" {
		t.Fatalf("persisted lock = %+v, want demo's resolved commit", lock)
	}

	code, objs = runJSON(t, "recipe", "sync")
	if code != ExitOK {
		t.Fatalf("sync exit = %d: %v", code, objs)
	}
	if _, err := os.Stat(filepath.Join(scope.CachePath, "demo", "recipe.toml")); err != nil {
		t.Fatalf("sync did not create the pinned cache: %v", err)
	}
}

func TestRecipeUpdateTargetedAndAllUseReturnedNames(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	demo := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo-v1\n")
	other := cliRecipeRepo(t, "other", "#!/bin/sh\necho other-v1\n")

	for _, src := range []string{demo, other} {
		if code, _ := runJSON(t, "recipe", "add", src, "-y", "--global"); code != ExitOK {
			t.Fatalf("add %s failed", src)
		}
	}
	scope, err := recipes.ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	demoV2 := testutil.GitCommit(t, demo, map[string]string{"install.sh": "#!/bin/sh\necho demo-v2\n"}, "")
	if code, _ := runJSON(t, "recipe", "update", "demo", "--global"); code != ExitOK {
		t.Fatal("targeted update failed")
	}
	after, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if after.Recipes["demo"].Commit != demoV2 {
		t.Fatalf("targeted update demo pin = %q, want %q", after.Recipes["demo"].Commit, demoV2)
	}
	if after.Recipes["other"].Commit != before.Recipes["other"].Commit {
		t.Fatal("targeted update changed the unrequested recipe")
	}

	otherV2 := testutil.GitCommit(t, other, map[string]string{"install.sh": "#!/bin/sh\necho other-v2\n"}, "")
	if code, _ := runJSON(t, "recipe", "update", "--global"); code != ExitOK {
		t.Fatal("all-recipes update failed")
	}
	after, err = scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if after.Recipes["other"].Commit != otherV2 || after.Recipes["demo"].Commit != demoV2 {
		t.Fatalf("all update pins = %+v, want both current commits", after.Recipes)
	}
}

func TestRecipeRMForceDoesNotBypassConfirmationAndRefusesVMUse(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	if code, _ := runJSON(t, "recipe", "add", src, "-y", "--global"); code != ExitOK {
		t.Fatal("add failed")
	}
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}).Save(); err != nil {
		t.Fatal(err)
	}

	code, objs := runJSON(t, "recipe", "rm", "demo", "--global")
	if code != ExitFail || result(t, objs)["error"].(map[string]any)["code"] != string(wire.CodeConfirmationRequired) {
		t.Fatalf("rm without -y = %d, %v; want confirmation_required", code, objs)
	}
	code, objs = runJSON(t, "recipe", "rm", "demo", "--global", "--force")
	if code != ExitFail || result(t, objs)["error"].(map[string]any)["code"] != string(wire.CodeConfirmationRequired) {
		t.Fatalf("--force without -y = %d, %v; want confirmation_required", code, objs)
	}
	if code, _ = runJSON(t, "recipe", "rm", "demo", "--global", "--force", "-y"); code != ExitOK {
		t.Fatal("--force -y should remove a recipe used by a VM")
	}
	scope, err := recipes.ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Recipes["demo"]; ok {
		t.Fatal("forced removal left the lock entry")
	}
}

func TestRecipeListShowsGlobalPinFromProjectScope(t *testing.T) {
	cliRoot(t)
	globalDir := t.TempDir()
	t.Chdir(globalDir)
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	if code, _ := runJSON(t, "recipe", "add", src, "-y", "--global"); code != ExitOK {
		t.Fatal("global add failed")
	}
	project := t.TempDir()
	writeCLIFile(t, filepath.Join(project, "stoat.toml"), "[recipes]\n")
	t.Chdir(project)
	code, objs := runJSON(t, "recipe", "list")
	if code != ExitOK {
		t.Fatalf("project list exit = %d", code)
	}
	for _, raw := range dataOf(t, objs)["recipes"].([]any) {
		row := raw.(map[string]any)
		if row["name"] == "demo" {
			if row["scope"] != "global" || row["source"] != src || row["commit"] == "" {
				t.Fatalf("global row lost pin metadata in project scope: %v", row)
			}
			return
		}
	}
	t.Fatal("project recipe list omitted the visible global recipe")
}

func TestRecipeURLJSONRefusalHasOneTypedEnvelopeWithoutPreviewProse(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	var out, errOut bytes.Buffer
	code := Main([]string{"--json", "recipe", "add", src, "--global"}, "test", strings.NewReader("y\n"), &out, &errOut)
	if code != ExitFail {
		t.Fatalf("URL add without -y exit = %d, want ExitFail", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("URL refusal wrote %d lines: %q", len(lines), out.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
		t.Fatalf("URL refusal line is not JSON: %v", err)
	}
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeConfirmationRequired) {
		t.Errorf("error.code = %v, want %q", errObj["code"], wire.CodeConfirmationRequired)
	}
	if strings.Contains(out.String(), "os:") || strings.Contains(out.String(), "requires:") {
		t.Errorf("JSON refusal leaked preview prose: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty under --json", errOut.String())
	}
}

func TestRecipeURLNonTTYDoesNotPromptOrMutate(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	var out, errOut bytes.Buffer
	code := Main([]string{"recipe", "add", src, "--global"}, "test", strings.NewReader("y\n"), &out, &errOut)
	if code != ExitFail {
		t.Fatalf("URL add on a non-TTY exit = %d, want ExitFail", code)
	}
	if strings.Contains(out.String(), "demo") || strings.Contains(out.String(), "os:") {
		t.Fatalf("non-TTY add printed preview/prompt prose: %q", out.String())
	}
	scope, err := recipes.ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Recipes) != 0 {
		t.Fatalf("non-TTY refusal mutated the lock: %+v", lock.Recipes)
	}
}

func TestRecipeAddIndexNameSkipsConfirmation(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	cliIndex(t, fmt.Sprintf("[recipes.demo]\nsource = %q\ndescription = \"demo\"\nos = [\"alpine\"]\n", src))
	if code, objs := runJSON(t, "recipe", "add", "demo", "--global"); code != ExitOK {
		t.Fatalf("index-name add without -y exit = %d: %v", code, objs)
	}
}

func TestRecipeVersionAndEmptyRemoteListsUseContractThreeAndArrays(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	cliIndex(t, "")
	code, objs := runJSON(t, "version")
	if code != ExitOK {
		t.Fatal("version failed")
	}
	version := dataOf(t, objs)["contract"]
	if version != float64(3) {
		t.Fatalf("version contract = %v, want 3", version)
	}
	for _, argv := range [][]string{
		{"recipe", "search", "no-match"},
		{"recipe", "lock", "--global"},
		{"recipe", "sync", "--global"},
	} {
		code, objs = runJSON(t, argv...)
		if code != ExitOK {
			t.Fatalf("%v exit = %d: %v", argv, code, objs)
		}
		if recipes, ok := dataOf(t, objs)["recipes"].([]any); !ok || recipes == nil {
			t.Fatalf("%v data.recipes = %#v, want non-null array", argv, dataOf(t, objs)["recipes"])
		}
	}
}

func TestRecipeApplyStaleProjectLockUsesTypedRepairCode(t *testing.T) {
	cliRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeCLIFile(t, filepath.Join(project, "stoat.toml"), "[recipes]\ndemo = \"main\"\n")
	if err := (&config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}).Save(); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "apply", "work", "--dry-run")
	if code != ExitFail {
		t.Fatalf("stale apply exit = %d, want ExitFail: %v", code, objs)
	}
	errObj := result(t, objs)["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeLockOutOfDate) {
		t.Fatalf("stale apply error.code = %v, want %q", errObj["code"], wire.CodeLockOutOfDate)
	}
}
