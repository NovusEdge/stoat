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

func TestRecipeRMPreflightsVMUseBeforeConfirmationAndPreservesDisk(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	if code, _ := runJSON(t, "recipe", "add", src, "-y", "--global"); code != ExitOK {
		t.Fatal("add failed")
	}
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201, Recipes: []string{"demo"}}).Save(); err != nil {
		t.Fatal(err)
	}
	scope, err := recipes.ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(scope.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	vmBefore, err := os.ReadFile(filepath.Join(config.Root(), "work", "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(scope.CachePath, "demo")
	if _, err := os.Stat(filepath.Join(cachePath, "recipe.toml")); err != nil {
		t.Fatalf("add did not create recipe cache: %v", err)
	}

	assertPreserved := func(label string) {
		t.Helper()
		lockAfter, readErr := os.ReadFile(scope.LockPath)
		if readErr != nil || string(lockAfter) != string(lockBefore) {
			t.Fatalf("%s changed lock: %v", label, readErr)
		}
		vmAfter, readErr := os.ReadFile(filepath.Join(config.Root(), "work", "vm.toml"))
		if readErr != nil || string(vmAfter) != string(vmBefore) {
			t.Fatalf("%s changed VM declaration: %v", label, readErr)
		}
		if _, readErr := os.Stat(filepath.Join(cachePath, "recipe.toml")); readErr != nil {
			t.Fatalf("%s removed recipe cache: %v", label, readErr)
		}
	}

	code, objs := runJSON(t, "recipe", "rm", "demo", "--global")
	errObj := result(t, objs)["error"].(map[string]any)
	if code != ExitFail || errObj["code"] != string(wire.CodeInUse) {
		t.Fatalf("in-use rm without -y = %d, %v; want in_use before confirmation", code, objs)
	}
	if message, _ := errObj["message"].(string); !strings.Contains(message, "work") || !strings.Contains(message, "other") {
		t.Fatalf("in-use message = %q, want every VM name", message)
	}
	assertPreserved("in-use rm without -y")

	code, objs = runJSON(t, "recipe", "rm", "demo", "--global", "-y")
	errObj = result(t, objs)["error"].(map[string]any)
	if code != ExitFail || errObj["code"] != string(wire.CodeInUse) {
		t.Fatalf("in-use rm with -y = %d, %v; want in_use", code, objs)
	}
	assertPreserved("in-use rm with -y")

	code, objs = runJSON(t, "recipe", "rm", "demo", "--global", "--force")
	if code != ExitFail || result(t, objs)["error"].(map[string]any)["code"] != string(wire.CodeConfirmationRequired) {
		t.Fatalf("--force without -y = %d, %v; want confirmation_required", code, objs)
	}
	assertPreserved("forced rm without -y")
	if code, _ = runJSON(t, "recipe", "rm", "demo", "--global", "--force", "-y"); code != ExitOK {
		t.Fatal("--force -y should remove a recipe used by a VM")
	}
	lock, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Recipes["demo"]; ok {
		t.Fatal("forced removal left the lock entry")
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("forced removal left recipe cache: %v", err)
	}

	free := cliRecipeRepo(t, "free", "#!/bin/sh\necho free\n")
	if code, _ := runJSON(t, "recipe", "add", free, "-y", "--global"); code != ExitOK {
		t.Fatal("free recipe add failed")
	}
	code, objs = runJSON(t, "recipe", "rm", "free", "--global")
	if code != ExitFail || result(t, objs)["error"].(map[string]any)["code"] != string(wire.CodeConfirmationRequired) {
		t.Fatalf("removable rm without -y = %d, %v; want confirmation_required", code, objs)
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

func TestRecipeSearchLiteralLeadingOptionsReachTheRealIndexQuery(t *testing.T) {
	cases := []struct {
		term string
		name string
	}{
		{term: "--json", name: "match-json"},
		{term: "--refresh", name: "match-refresh"},
		{term: "--", name: "match-terminator"},
	}
	for _, tc := range cases {
		t.Run(tc.term, func(t *testing.T) {
			cliRoot(t)
			t.Chdir(t.TempDir())
			cliIndex(t, fmt.Sprintf(
				"[recipes.%s]\nsource = \"local\"\ndescription = %q\nos = [\"alpine\"]\n\n[recipes.other]\nsource = \"local\"\ndescription = \"not this term\"\nos = [\"alpine\"]\n",
				tc.name, tc.term,
			))

			code, objs := runJSON(t, "recipe", "search", "--", tc.term)
			if code != ExitOK {
				t.Fatalf("search %q exit = %d: %v", tc.term, code, objs)
			}
			rows, _ := dataOf(t, objs)["recipes"].([]any)
			if len(rows) != 1 {
				t.Fatalf("search %q returned %d rows %v, want only its matching fixture", tc.term, len(rows), rows)
			}
			row, _ := rows[0].(map[string]any)
			if row["name"] != tc.name || row["description"] != tc.term {
				t.Fatalf("search %q row = %v, want %q/%q", tc.term, row, tc.name, tc.term)
			}
		})
	}
}

func TestRecipeJSONRemoteResultShapesUseFullPinsAndMinimalRemoval(t *testing.T) {
	t.Run("empty batch", func(t *testing.T) {
		cliRoot(t)
		t.Chdir(t.TempDir())
		code, objs := runJSON(t, "recipe", "update", "--global")
		if code != ExitOK {
			t.Fatalf("empty update exit = %d: %v", code, objs)
		}
		data := dataOf(t, objs)
		if len(data) != 1 {
			t.Fatalf("empty update data = %v, want exactly recipes", data)
		}
		rows, ok := data["recipes"].([]any)
		if !ok || rows == nil || len(rows) != 0 {
			t.Fatalf("empty update recipes = %#v, want []", data["recipes"])
		}
	})

	t.Run("add list update remove", func(t *testing.T) {
		cliRoot(t)
		t.Chdir(t.TempDir())
		src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho v1\n")

		code, objs := runJSON(t, "recipe", "add", src, "-y", "--global")
		if code != ExitOK {
			t.Fatalf("add exit = %d: %v", code, objs)
		}
		add := dataOf(t, objs)
		if got := len(add); got != 5 {
			t.Fatalf("add data has %d fields (%v), want name/source/ref/commit/scope", got, add)
		}
		for _, key := range []string{"name", "source", "ref", "commit", "scope"} {
			if _, ok := add[key]; !ok {
				t.Errorf("add data omitted %q: %v", key, add)
			}
		}
		if add["name"] != "demo" || add["source"] != src || add["scope"] != "global" {
			t.Errorf("add metadata = %v", add)
		}
		addCommit, _ := add["commit"].(string)
		if len(addCommit) != 40 {
			t.Fatalf("add commit = %q, want full commit", addCommit)
		}

		code, objs = runJSON(t, "recipe", "list")
		if code != ExitOK {
			t.Fatalf("list exit = %d: %v", code, objs)
		}
		list := dataOf(t, objs)
		rows, _ := list["recipes"].([]any)
		var row map[string]any
		for _, raw := range rows {
			candidate, _ := raw.(map[string]any)
			if candidate["name"] == "demo" {
				row = candidate
				break
			}
		}
		if row == nil {
			t.Fatalf("list recipes omitted demo: %#v", list["recipes"])
		}
		if len(row) != 6 || row["name"] != "demo" || row["description"] != "demo recipe" || row["source"] != src || row["scope"] != "global" {
			t.Fatalf("list row = %v, want named pin metadata", row)
		}
		listCommit, _ := row["commit"].(string)
		if len(listCommit) != 7 || listCommit != addCommit[:7] {
			t.Fatalf("list commit = %q, want seven-character prefix %q", listCommit, addCommit[:7])
		}

		v2 := testutil.GitCommit(t, src, map[string]string{"install.sh": "#!/bin/sh\necho v2\n"}, "")
		code, objs = runJSON(t, "recipe", "update", "demo", "--global")
		if code != ExitOK {
			t.Fatalf("update exit = %d: %v", code, objs)
		}
		update := dataOf(t, objs)
		updateRows, _ := update["recipes"].([]any)
		if len(updateRows) != 1 {
			t.Fatalf("update rows = %#v, want one row", update["recipes"])
		}
		updated, _ := updateRows[0].(map[string]any)
		updatedCommit, _ := updated["commit"].(string)
		if len(updatedCommit) != 40 || updatedCommit != v2 {
			t.Fatalf("update commit = %q, want full %q", updatedCommit, v2)
		}

		code, objs = runJSON(t, "recipe", "rm", "demo", "--global", "-y")
		if code != ExitOK {
			t.Fatalf("remove exit = %d: %v", code, objs)
		}
		removed := dataOf(t, objs)
		if len(removed) != 2 || removed["name"] != "demo" || removed["scope"] != "global" {
			t.Fatalf("remove data = %v, want exactly name and scope", removed)
		}
		for _, forbidden := range []string{"source", "ref", "commit"} {
			if _, present := removed[forbidden]; present {
				t.Errorf("remove data exposed empty %q field: %v", forbidden, removed)
			}
		}
	})
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
