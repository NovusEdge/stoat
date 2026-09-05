package recipes

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/gitx"
	"github.com/novusedge/stoat/internal/tomlx"
)

// ErrDirty identifies a cache checkout with uncommitted changes.
var ErrDirty = errors.New("local changes")

// ParseRef splits an index name or repository URL from its optional ref.
// Scp-style URLs retain the username because their first at-sign is followed
// by a host and colon, not by a ref.
func ParseRef(in string) (source, gitRef string, isURL bool) {
	source = in
	if i := strings.LastIndexByte(in, '@'); i > 0 && i+1 < len(in) {
		candidateSource, candidateRef := in[:i], in[i+1:]
		sourceLike := strings.Contains(candidateSource, "://") || strings.Contains(candidateSource, ":") || strings.Contains(candidateSource, "/") ||
			strings.HasPrefix(candidateSource, "/") || strings.HasPrefix(candidateSource, ".")
		indexLike := !strings.ContainsAny(candidateSource, "/:") && !strings.HasPrefix(candidateSource, ".")
		authorityAt := false
		if scheme := strings.Index(in, "://"); scheme >= 0 {
			authorityStart := scheme + len("://")
			authorityEnd := strings.IndexByte(in[authorityStart:], '/')
			if authorityEnd < 0 {
				authorityEnd = len(in) - authorityStart
			}
			authorityAt = i < authorityStart+authorityEnd
		}
		// An scp-style source has the form user@host:path. Its colon is
		// part of the source unless the source itself already contains path
		// syntax before a second at-sign carrying the requested ref.
		if !authorityAt && (sourceLike || (indexLike && !strings.Contains(candidateRef, ":"))) {
			source, gitRef = candidateSource, candidateRef
		}
	}
	isURL = strings.Contains(source, "://") || strings.Contains(source, ":") ||
		strings.Contains(source, "/") || strings.HasPrefix(source, ".") || strings.HasSuffix(source, ".git")
	return source, gitRef, isURL
}

func nameFromURL(source string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(source, "/"), ".git")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimPrefix(name, "stoat-")
}

func resolveSource(in string) (name, source, gitRef string, err error) {
	source, gitRef, isURL := ParseRef(in)
	if isURL {
		return nameFromURL(source), source, gitRef, nil
	}
	entry, ok, err := IndexLookup(source)
	if err != nil {
		return "", "", "", err
	}
	if !ok {
		return "", "", "", fmt.Errorf("no recipe %q in the index; run stoat recipe search %s", source, source)
	}
	return entry.Name, entry.Source, gitRef, nil
}

// Preview clones a source into a temporary directory and parses its manifest.
// The returned directory remains available to the caller until it removes it.
func Preview(source, gitRef string) (Manifest, string, error) {
	tmp, err := os.MkdirTemp("", "stoat-preview-")
	if err != nil {
		return Manifest{}, "", err
	}
	dst := filepath.Join(tmp, "recipe")
	if err := gitx.Clone(source, gitRef, dst); err != nil {
		if removeErr := os.RemoveAll(tmp); removeErr != nil {
			return Manifest{}, "", fmt.Errorf("%w; remove preview: %v", refError(source, gitRef, err), removeErr)
		}
		return Manifest{}, "", refError(source, gitRef, err)
	}
	m, err := ParseManifest(filepath.Join(dst, "recipe.toml"))
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file or directory") {
			err = fmt.Errorf("%s: no recipe.toml at the repository root", source)
		}
		if removeErr := os.RemoveAll(tmp); removeErr != nil {
			return Manifest{}, "", fmt.Errorf("%w; remove preview: %v", err, removeErr)
		}
		return Manifest{}, "", err
	}
	return m, tmp, nil
}

func refError(source, gitRef string, err error) error {
	if errors.Is(err, gitx.ErrNoRef) {
		return fmt.Errorf("%s: no tag or branch %q", refLabel(source), gitRef)
	}
	return err
}

func refLabel(source string) string {
	if strings.Contains(source, "://") {
		if parsed, err := url.Parse(source); err == nil && parsed.Path != "" {
			return strings.TrimSuffix(strings.TrimPrefix(strings.Trim(parsed.Path, "/"), "/"), ".git")
		}
	}
	if colon := strings.IndexByte(source, ':'); colon >= 0 && !strings.Contains(source[:colon], "/") {
		source = source[colon+1:]
	}
	return strings.TrimSuffix(strings.Trim(source, "/"), ".git")
}

// Add stages a validated checkout and all related files before replacing the
// active cache, lock, declaration, and gitignore entries.
func Add(s Scope, in string, force bool) (LockEntry, error) {
	name, source, gitRef, err := resolveSource(in)
	if err != nil {
		return LockEntry{}, err
	}
	unlock, err := lockScope(s)
	if err != nil {
		return LockEntry{}, err
	}
	defer func() { _ = unlock() }()
	if !force {
		if err := CheckCollision(name, s.Name); err != nil {
			return LockEntry{}, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(s.CachePath), 0o755); err != nil {
		return LockEntry{}, err
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(s.CachePath), ".stoat-recipe-add-*")
	if err != nil {
		return LockEntry{}, err
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()
	stageCache := filepath.Join(stageRoot, name)
	if err := gitx.Clone(source, gitRef, stageCache); err != nil {
		return LockEntry{}, refError(source, gitRef, err)
	}
	if err := ValidateTree(stageCache, name); err != nil {
		return LockEntry{}, err
	}
	commit, err := gitx.RevParse(stageCache, "HEAD")
	if err != nil {
		return LockEntry{}, err
	}
	entry := LockEntry{Name: name, Source: source, Ref: gitRef, Commit: commit, Added: time.Now().UTC().Format(time.RFC3339)}

	lock, err := s.Lock()
	if err != nil {
		return LockEntry{}, err
	}
	if lock.Recipes == nil {
		lock.Recipes = map[string]LockEntry{}
	}
	persistEntry := entry
	persistEntry.Name = ""
	lock.Recipes[name] = persistEntry
	artifacts, cleanup, err := prepareAddArtifacts(s, name, lock, in, source, gitRef, stageCache, stageRoot)
	if err != nil {
		return LockEntry{}, err
	}
	defer func() { _ = cleanup() }()
	if err := publishArtifacts(artifacts); err != nil {
		return LockEntry{}, err
	}
	return entry, nil
}

type artifact struct {
	target string
	stage  string
	isDir  bool
	remove bool
}

type publishedArtifact struct {
	artifact
	backup      string
	oldExists   bool
	targetMoved bool
	published   bool
}

// These defaults are the standard filesystem operations. Tests can replace
// them briefly to induce a deterministic publication or backup-cleanup fault
// through Add/Remove and then restore the defaults with t.Cleanup; production
// behavior remains the direct os.Rename/os.RemoveAll path.
var transactionRename = os.Rename
var transactionRemoveBackup = func(target, backup string) error {
	_ = target
	return os.RemoveAll(backup)
}

func prepareAddArtifacts(s Scope, name string, lock Lock, input, source, gitRef, stageCache, stageRoot string) ([]artifact, func() error, error) {
	var artifacts []artifact
	lockStageDir, err := os.MkdirTemp(filepath.Dir(s.LockPath), ".stoat-lock-stage-*")
	if err != nil {
		return nil, func() error { return nil }, err
	}
	cleanup := func() error {
		return os.RemoveAll(lockStageDir)
	}
	lockStage := filepath.Join(lockStageDir, "stoat.lock")
	if err := SaveLock(lockStage, lock); err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	if mode, modeErr := existingFileMode(s.LockPath); modeErr != nil {
		_ = cleanup()
		return nil, func() error { return nil }, modeErr
	} else if mode != 0 {
		if err := os.Chmod(lockStage, mode); err != nil {
			_ = cleanup()
			return nil, func() error { return nil }, err
		}
	}
	artifacts = append(artifacts, artifact{target: s.LockPath, stage: lockStage})

	if s.Name != "project" {
		return append(artifacts, artifact{target: filepath.Join(s.CachePath, name), stage: stageCache, isDir: true}), cleanup, nil
	}
	decls, err := s.Decls()
	if err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	if _, _, isURL := ParseRef(input); isURL {
		decls[name] = Decl{Source: source, Ref: gitRef}
	} else {
		decls[name] = Decl{Ref: gitRef}
	}
	var project map[string]any
	if err := tomlx.Decode(s.ConfigPath, &project, tomlx.Warn(io.Discard)); err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	raw := make(map[string]any, len(decls))
	for n, d := range decls {
		if d.Source == "" {
			raw[n] = d.Ref
		} else {
			raw[n] = map[string]any{"source": d.Source, "ref": d.Ref}
		}
	}
	project["recipes"] = raw
	projectStageDir, err := os.MkdirTemp(filepath.Dir(s.ConfigPath), ".stoat-project-stage-*")
	if err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	oldMode, err := existingFileMode(s.ConfigPath)
	if err != nil {
		_ = os.RemoveAll(projectStageDir)
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	projectStage := filepath.Join(projectStageDir, "stoat.toml")
	if err := tomlx.Encode(projectStage, project); err != nil {
		_ = os.RemoveAll(projectStageDir)
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	if oldMode != 0 {
		if err := os.Chmod(projectStage, oldMode); err != nil {
			_ = os.RemoveAll(projectStageDir)
			_ = cleanup()
			return nil, func() error { return nil }, err
		}
	}
	cleanup = joinCleanup(cleanup, func() error { return os.RemoveAll(projectStageDir) })
	artifacts = append(artifacts, artifact{target: s.ConfigPath, stage: projectStage})

	ignore, err := prepareIgnoreArtifact(s.Dir, stageRoot)
	if err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, err
	}
	if ignore != nil {
		artifacts = append(artifacts, *ignore)
	}
	return append(artifacts, artifact{target: filepath.Join(s.CachePath, name), stage: stageCache, isDir: true}), cleanup, nil
}

func joinCleanup(first, second func() error) func() error {
	return func() error {
		err1 := first()
		err2 := second()
		if err1 != nil && err2 != nil {
			return fmt.Errorf("%v; %v", err1, err2)
		}
		if err1 != nil {
			return err1
		}
		return err2
	}
}

func prepareIgnoreArtifact(dir, stageRoot string) (*artifact, error) {
	gitPath := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	path := filepath.Join(dir, ".gitignore")
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == ".stoat/" {
			return nil, nil
		}
	}
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		body = append(body, '\n')
	}
	body = append(body, ".stoat/\n"...)
	stage := filepath.Join(stageRoot, ".gitignore")
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err := os.WriteFile(stage, body, mode); err != nil {
		return nil, err
	}
	if err := os.Chmod(stage, mode); err != nil {
		return nil, err
	}
	return &artifact{target: path, stage: stage}, nil
}

func publishArtifacts(artifacts []artifact) error {
	published := make([]publishedArtifact, 0, len(artifacts))
	for _, a := range artifacts {
		if err := os.MkdirAll(filepath.Dir(a.target), 0o755); err != nil {
			return rollbackPublished(published, err)
		}
		p := publishedArtifact{artifact: a}
		if _, err := os.Lstat(a.target); err == nil {
			backup, backupErr := makeBackupPath(filepath.Dir(a.target))
			if backupErr != nil {
				return rollbackPublished(published, backupErr)
			}
			if err := transactionRename(a.target, backup); err != nil {
				_ = os.Remove(backup)
				return rollbackPublished(published, err)
			}
			p.backup, p.oldExists = backup, true
		} else if !os.IsNotExist(err) {
			return rollbackPublished(published, err)
		}
		p.targetMoved = p.oldExists
		published = append(published, p)
		if a.remove {
			p.published = true
			published[len(published)-1] = p
			continue
		}
		if err := transactionRename(a.stage, a.target); err != nil {
			return rollbackPublished(published, err)
		}
		p.published = true
		published[len(published)-1] = p
	}
	for _, p := range published {
		if p.oldExists {
			if err := transactionRemoveBackup(p.target, p.backup); err != nil {
				return fmt.Errorf("published recipe changes; old artifact backup %s remains: %w", p.backup, err)
			}
		}
	}
	return nil
}

func rollbackPublished(published []publishedArtifact, cause error) error {
	var rollbackErr error
	for i := len(published) - 1; i >= 0; i-- {
		p := published[i]
		if !p.targetMoved && !p.published {
			continue
		}
		if p.published {
			if err := os.RemoveAll(p.target); err != nil && rollbackErr == nil {
				rollbackErr = err
				continue
			}
		}
		if p.oldExists {
			if err := transactionRename(p.backup, p.target); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
	}
	return cause
}

func makeBackupPath(parent string) (string, error) {
	dir, err := os.MkdirTemp(parent, ".stoat-recipe-backup-*")
	if err != nil {
		return "", err
	}
	if err := os.Remove(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func lockScope(s Scope) (func() error, error) {
	coordinationDir := s.Dir
	if s.Name == "project" {
		coordinationDir = filepath.Dir(s.CachePath)
	}
	if err := os.MkdirAll(coordinationDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(coordinationDir, "recipe.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

// LockAll resolves every project declaration to a fresh commit and persists
// the result under the scope coordination lock. It does not touch the cache.
func LockAll(s Scope) (Lock, error) {
	unlock, err := lockScope(s)
	if err != nil {
		return Lock{}, err
	}
	defer func() { _ = unlock() }()
	old, err := s.Lock()
	if err != nil {
		return Lock{}, err
	}
	decls, err := s.Decls()
	if err != nil {
		return Lock{}, err
	}
	if s.Name == "global" {
		decls = make(map[string]Decl, len(old.Recipes))
		for name, entry := range old.Recipes {
			decls[name] = Decl{Source: entry.Source, Ref: entry.Ref}
		}
	}
	names := make([]string, 0, len(decls))
	for name := range decls {
		names = append(names, name)
	}
	sort.Strings(names)
	next := Lock{Schema: LockSchema, Recipes: make(map[string]LockEntry, len(names))}
	for _, name := range names {
		decl := decls[name]
		source := decl.Source
		if source == "" {
			entry, ok, lookupErr := IndexLookup(name)
			if lookupErr != nil {
				return Lock{}, lookupErr
			}
			if !ok {
				return Lock{}, fmt.Errorf("no recipe %q in the index; run stoat recipe search %s", name, name)
			}
			source = entry.Source
		}
		commit, resolveErr := resolveCommit(source, decl.Ref)
		if resolveErr != nil {
			return Lock{}, refError(source, decl.Ref, resolveErr)
		}
		added := time.Now().UTC().Format(time.RFC3339)
		if previous, ok := old.Recipes[name]; ok && previous.Added != "" {
			added = previous.Added
		}
		next.Recipes[name] = LockEntry{Source: source, Ref: decl.Ref, Commit: commit, Added: added}
	}
	if err := s.Save(next); err != nil {
		return Lock{}, err
	}
	return s.Lock()
}

func resolveCommit(source, gitRef string) (string, error) {
	tmp, err := os.MkdirTemp("", "stoat-lock-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dst := filepath.Join(tmp, "recipe")
	if err := gitx.Clone(source, gitRef, dst); err != nil {
		return "", refError(source, gitRef, err)
	}
	return gitx.RevParse(dst, "HEAD")
}

// Sync stages every missing or mismatched checkout, validates them, then
// publishes the complete cache transaction. Project caches remove stray
// entries; the global cache leaves non-remote recipes untouched.
func Sync(s Scope) error {
	unlock, err := lockScope(s)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	lock, err := s.Lock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.CachePath), 0o755); err != nil {
		return err
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(s.CachePath), ".stoat-recipe-sync-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()

	names := make([]string, 0, len(lock.Recipes))
	for name := range lock.Recipes {
		names = append(names, name)
	}
	sort.Strings(names)
	artifacts := make([]artifact, 0, len(names))
	for _, name := range names {
		entry := lock.Recipes[name]
		dst := filepath.Join(s.CachePath, name)
		matches, matchErr := cacheMatches(dst, name, entry)
		if matchErr != nil {
			return matchErr
		}
		if matches {
			continue
		}
		stage := filepath.Join(stageRoot, name)
		if err := gitx.CloneFull(entry.Source, stage); err != nil {
			return err
		}
		if err := gitx.Checkout(stage, entry.Commit); err != nil {
			return err
		}
		if err := ValidateTree(stage, name); err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{target: dst, stage: stage, isDir: true})
	}
	if s.Name == "project" {
		entries, readErr := os.ReadDir(s.CachePath)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if _, ok := lock.Recipes[entry.Name()]; !ok {
				artifacts = append(artifacts, artifact{target: filepath.Join(s.CachePath, entry.Name()), remove: true, isDir: true})
			}
		}
	}
	return publishArtifacts(artifacts)
}

func cacheMatches(path, name string, entry LockEntry) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	dirty, err := gitx.Dirty(path)
	if err != nil {
		return false, err
	}
	if dirty {
		return false, fmt.Errorf("%s: %w; copy it to a local recipe first", name, ErrDirty)
	}
	have, err := gitx.RevParse(path, "HEAD")
	if err != nil {
		return false, err
	}
	if have != entry.Commit {
		return false, nil
	}
	if err := ValidateTree(path, name); err != nil {
		return false, err
	}
	return true, nil
}

// StaleLock reports the first project declaration that is absent or differs
// from its lock pin. Global scope has no declaration and is never stale here.
func StaleLock(s Scope) (string, bool, error) {
	decls, err := s.Decls()
	if err != nil {
		return "", false, err
	}
	lock, err := s.Lock()
	if err != nil {
		return "", false, err
	}
	names := make([]string, 0, len(decls))
	for name := range decls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		decl := decls[name]
		entry, ok := lock.Recipes[name]
		if !ok || entry.Ref != decl.Ref || (decl.Source != "" && entry.Source != decl.Source) {
			return name, true, nil
		}
	}
	return "", false, nil
}

// Update stages every requested ref, validates every resulting tree, and
// publishes the cache and lock together. A dirty or unreadable checkout is
// never replaced implicitly.
func Update(s Scope, names []string) ([]LockEntry, error) {
	unlock, err := lockScope(s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()
	lock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		for name := range lock.Recipes {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(s.CachePath), ".stoat-recipe-update-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()
	artifacts := make([]artifact, 0, len(names)+1)
	result := make([]LockEntry, 0, len(names))
	for _, name := range names {
		entry, ok := lock.Recipes[name]
		if !ok {
			return nil, fmt.Errorf("%s is not a remote recipe in this scope", name)
		}
		dir := filepath.Join(s.CachePath, name)
		if _, statErr := os.Lstat(dir); statErr != nil {
			if !os.IsNotExist(statErr) {
				return nil, statErr
			}
		} else {
			dirty, dirtyErr := gitx.Dirty(dir)
			if dirtyErr != nil {
				return nil, dirtyErr
			}
			if dirty {
				return nil, fmt.Errorf("%s: %w; copy it to a local recipe first", name, ErrDirty)
			}
		}
		stage := filepath.Join(stageRoot, name)
		if err := gitx.Clone(entry.Source, entry.Ref, stage); err != nil {
			return nil, refError(entry.Source, entry.Ref, err)
		}
		if err := ValidateTree(stage, name); err != nil {
			return nil, err
		}
		commit, err := gitx.RevParse(stage, "HEAD")
		if err != nil {
			return nil, err
		}
		entry.Name = name
		entry.Commit = commit
		persistEntry := entry
		persistEntry.Name = ""
		lock.Recipes[name] = persistEntry
		result = append(result, entry)
		artifacts = append(artifacts, artifact{target: dir, stage: stage, isDir: true})
	}
	lockArtifact, lockCleanup, err := prepareLockArtifact(s, lock, stageRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lockCleanup() }()
	artifacts = append(artifacts, lockArtifact)
	if err := publishArtifacts(artifacts); err != nil {
		return nil, err
	}
	return result, nil
}

// Remove stages a lock, declaration, and cache removal before publishing any
// of them. A malformed declaration or persistence failure leaves all three
// active artifacts in place.
func Remove(s Scope, name string) error {
	unlock, err := lockScope(s)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	lock, err := s.Lock()
	if err != nil {
		return err
	}
	if _, ok := lock.Recipes[name]; !ok {
		return fmt.Errorf("%s is not a remote recipe in this scope", name)
	}
	delete(lock.Recipes, name)
	stageRoot, err := os.MkdirTemp(filepath.Dir(s.CachePath), ".stoat-recipe-remove-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stageRoot) }()
	lockArtifact, lockCleanup, err := prepareLockArtifact(s, lock, stageRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lockCleanup() }()
	artifacts := []artifact{lockArtifact, {target: filepath.Join(s.CachePath, name), remove: true, isDir: true}}
	if s.Name == "project" {
		projectArtifact, projectCleanup, err := prepareProjectWithout(s, name, stageRoot)
		if err != nil {
			return err
		}
		defer func() { _ = projectCleanup() }()
		artifacts = append(artifacts, projectArtifact)
	}
	return publishArtifacts(artifacts)
}

func prepareLockArtifact(s Scope, lock Lock, stageRoot string) (artifact, func() error, error) {
	stageDir, err := os.MkdirTemp(filepath.Dir(s.LockPath), ".stoat-lock-stage-*")
	if err != nil {
		return artifact{}, func() error { return nil }, err
	}
	cleanup := func() error { return os.RemoveAll(stageDir) }
	stage := filepath.Join(stageDir, "stoat.lock")
	if err := SaveLock(stage, lock); err != nil {
		_ = cleanup()
		return artifact{}, func() error { return nil }, err
	}
	if mode, err := existingFileMode(s.LockPath); err != nil {
		_ = cleanup()
		return artifact{}, func() error { return nil }, err
	} else if mode != 0 {
		if err := os.Chmod(stage, mode); err != nil {
			_ = cleanup()
			return artifact{}, func() error { return nil }, err
		}
	}
	return artifact{target: s.LockPath, stage: stage}, cleanup, nil
}

func prepareProjectWithout(s Scope, name, stageRoot string) (artifact, func() error, error) {
	var project map[string]any
	if err := tomlx.Decode(s.ConfigPath, &project, tomlx.Warn(io.Discard)); err != nil {
		return artifact{}, func() error { return nil }, err
	}
	raw, ok := project["recipes"].(map[string]any)
	if !ok {
		return artifact{}, func() error { return nil }, fmt.Errorf("%s: recipes must be a table", ProjectFile)
	}
	delete(raw, name)
	project["recipes"] = raw
	stageDir, err := os.MkdirTemp(filepath.Dir(s.ConfigPath), ".stoat-project-stage-*")
	if err != nil {
		return artifact{}, func() error { return nil }, err
	}
	cleanup := func() error { return os.RemoveAll(stageDir) }
	stage := filepath.Join(stageDir, "stoat.toml")
	if err := tomlx.Encode(stage, project); err != nil {
		_ = cleanup()
		return artifact{}, func() error { return nil }, err
	}
	if mode, err := existingFileMode(s.ConfigPath); err != nil {
		_ = cleanup()
		return artifact{}, func() error { return nil }, err
	} else if mode != 0 {
		if err := os.Chmod(stage, mode); err != nil {
			_ = cleanup()
			return artifact{}, func() error { return nil }, err
		}
	}
	return artifact{target: s.ConfigPath, stage: stage}, cleanup, nil
}
