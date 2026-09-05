package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/gitx"
	"github.com/novusedge/stoat/internal/hostcheck"
	"github.com/novusedge/stoat/internal/recipes"
)

const shortSHA = 7

func short(sha string) string {
	if len(sha) > shortSHA {
		return sha[:shortSHA]
	}
	return sha
}

func runRecipeAdd(a *Args, stdin io.Reader, stdout, stderr io.Writer) int {
	s, err := recipes.ScopeFor(a.Global)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	source, gitRef, isURL := recipes.ParseRef(a.Ref)
	if isURL && !a.Yes {
		if a.JSON || !terminal(stdin) || !terminal(stdout) {
			_, code := confirm(a, stdin, stdout, stderr, "install this recipe; pass -y to confirm")
			return code
		}
		m, tmp, previewErr := recipes.Preview(source, gitRef)
		if previewErr != nil {
			return a.fail(stdout, stderr, previewErr)
		}
		if removeErr := os.RemoveAll(tmp); removeErr != nil {
			return a.fail(stdout, stderr, removeErr)
		}
		fmt.Fprintf(stdout, "name: %s\n", m.Name)
		fmt.Fprintf(stdout, "os: %s\n", strings.Join(m.OS, ", "))
		fmt.Fprintf(stdout, "requires: %s\n", strings.Join(m.Requires, ", "))
		for _, p := range m.SortedParams() {
			fmt.Fprintf(stdout, "param: %s (%s)\n", p.Name, p.Type)
		}
		if ok, code := confirm(a, stdin, stdout, stderr, "install "+m.Name+" from "+source+"?"); !ok {
			return code
		}
	}
	e, err := recipes.Add(s, a.Ref, a.Force)
	if err != nil {
		return a.fail(stdout, stderr, recipeGitError(err, "add"))
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeAdded{Name: e.Name, Source: e.Source, Ref: e.Ref, Commit: e.Commit, Scope: s.Name})
	}
	fmt.Fprintf(stdout, "%s %s (%s)\n", e.Name, e.Ref, short(e.Commit))
	return ExitOK
}

func runRecipeLock(a *Args, stdout, stderr io.Writer) int {
	s, err := recipes.ScopeFor(a.Global)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	l, err := recipes.LockAll(s)
	if err != nil {
		return a.fail(stdout, stderr, recipeGitError(err, "lock"))
	}
	return reportLock(a, stdout, s, l)
}

func runRecipeSync(a *Args, stdout, stderr io.Writer) int {
	s, err := recipes.ScopeFor(a.Global)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if err := recipes.Sync(s); err != nil {
		return a.fail(stdout, stderr, recipeGitError(err, "sync"))
	}
	l, err := recipes.ReadLock(s)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	return reportLock(a, stdout, s, l)
}

func reportLock(a *Args, stdout io.Writer, s recipes.Scope, l recipes.Lock) int {
	rows := make([]wire.RecipeAdded, 0, len(l.Recipes))
	names := make([]string, 0, len(l.Recipes))
	for name := range l.Recipes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := l.Recipes[name]
		rows = append(rows, wire.RecipeAdded{Name: name, Source: e.Source, Ref: e.Ref, Commit: e.Commit, Scope: s.Name})
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeBatch{Recipes: rows})
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%-20s %-12s %s\n", row.Name, row.Ref, short(row.Commit))
	}
	return ExitOK
}

func runRecipeUpdate(a *Args, stdout, stderr io.Writer) int {
	s, err := recipes.ScopeFor(a.Global)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	entries, err := recipes.Update(s, a.Names)
	if err != nil {
		return a.fail(stdout, stderr, recipeGitError(err, "update"))
	}
	rows := make([]wire.RecipeAdded, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, wire.RecipeAdded{Name: e.Name, Source: e.Source, Ref: e.Ref, Commit: e.Commit, Scope: s.Name})
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeBatch{Recipes: rows})
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%-20s %-12s %s\n", row.Name, row.Ref, short(row.Commit))
	}
	return ExitOK
}

func runRecipeRM(a *Args, stdin io.Reader, stdout, stderr io.Writer) int {
	name := a.Names[0]
	s, err := recipes.ScopeFor(a.Global)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	lock, err := recipes.ReadLock(s)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if _, ok := lock.Recipes[name]; !ok {
		return a.failMsg(stdout, stderr, core.ErrNotFound,
			fmt.Sprintf("%s is not a remote recipe in %s scope", name, s.Name))
	}
	if !a.Force {
		users, err := core.RecipeUsers(name)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if len(users) > 0 {
			return a.failMsg(stdout, stderr, core.ErrInUse,
				fmt.Sprintf("%s is used by %s; pass --force to remove it anyway", name, strings.Join(users, ", ")))
		}
	}
	if ok, code := confirm(a, stdin, stdout, stderr, "remove recipe "+name+"?"); !ok {
		return code
	}
	var users func() ([]string, error)
	if !a.Force {
		users = func() ([]string, error) { return core.RecipeUsers(name) }
	}
	if err := recipes.RemoveChecked(s, name, users); err != nil {
		var inUse *recipes.RemoveInUse
		if errors.As(err, &inUse) {
			return a.failMsg(stdout, stderr, core.ErrInUse,
				fmt.Sprintf("%s is used by %s; pass --force to remove it anyway", name, strings.Join(inUse.Users, ", ")))
		}
		return a.fail(stdout, stderr, recipeGitError(err, "remove"))
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeRemoved{Name: name, Scope: s.Name})
	}
	fmt.Fprintln(stdout, "removed", name)
	return ExitOK
}

func runRecipeSearch(a *Args, stdout, stderr io.Writer) int {
	if a.Refresh {
		if err := recipes.RefreshIndex(true); err != nil {
			return a.fail(stdout, stderr, err)
		}
	}
	hits, err := recipes.SearchIndex(a.Ref)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeSearch{Recipes: wire.FromIndexEntries(hits)})
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "no matches")
		return ExitOK
	}
	for _, h := range hits {
		fmt.Fprintf(stdout, "%-20s %s\n", h.Name, h.Description)
	}
	return ExitOK
}

func recipeGitError(err error, operation string) error {
	if !errors.Is(err, gitx.ErrNoGit) {
		return err
	}
	fix := "install git"
	for _, check := range hostcheck.RunChecks(hostcheck.DetectDistro()) {
		if check.Name == "git" && len(check.Fix) > 0 {
			fix = strings.Join(check.Fix, " && ")
			break
		}
	}
	return fmt.Errorf("%w: git is required for recipe %s; install it: %s", gitx.ErrNoGit, operation, fix)
}
