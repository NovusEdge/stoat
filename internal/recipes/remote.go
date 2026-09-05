package recipes

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if i := strings.LastIndexByte(in, '@'); i > 0 && i+1 < len(in) && !strings.ContainsAny(in[i+1:], "/:") {
		source, gitRef = in[:i], in[i+1:]
	}
	isURL = strings.Contains(source, "://") || strings.Contains(source, ":") ||
		strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".")
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
		return fmt.Errorf("%s: no tag or branch %q", nameFromURL(source), gitRef)
	}
	return err
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
}

type publishedArtifact struct {
	artifact
	backup      string
	oldExists   bool
	targetMoved bool
	published   bool
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
			if err := os.Rename(a.target, backup); err != nil {
				_ = os.Remove(backup)
				return rollbackPublished(published, err)
			}
			p.backup, p.oldExists = backup, true
		} else if !os.IsNotExist(err) {
			return rollbackPublished(published, err)
		}
		p.targetMoved = p.oldExists
		published = append(published, p)
		if err := os.Rename(a.stage, a.target); err != nil {
			return rollbackPublished(published, err)
		}
		p.published = true
		published[len(published)-1] = p
	}
	for _, p := range published {
		if p.oldExists {
			if err := os.RemoveAll(p.backup); err != nil {
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
			if err := os.Rename(p.backup, p.target); err != nil && rollbackErr == nil {
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
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".stoat-recipe.lock"), os.O_CREATE|os.O_RDWR, 0o644)
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

// The lock, sync, update, and remove operations are implemented in the next
// remote-recipe tasks. These declarations keep the test-first branch buildable.
func LockAll(Scope) (Lock, error) { return Lock{}, errors.New("remote recipe lock is not implemented") }

func Sync(Scope) error { return errors.New("remote recipe sync is not implemented") }

func StaleLock(Scope) (string, bool, error) {
	return "", false, errors.New("remote recipe lock is not implemented")
}

func Update(Scope, []string) ([]LockEntry, error) {
	return nil, errors.New("remote recipe update is not implemented")
}

func Remove(Scope, string) error { return errors.New("remote recipe remove is not implemented") }
