// Package mcpsrv serves the MCP protocol from the stoat binary. Every tool
// runs the guards in this file, calls internal/core, and returns a wire DTO.
package mcpsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// A VM name becomes a directory name under the data root, so the pattern is
// what keeps an operation inside it. Rejecting beats sanitizing: a rewrite
// hides the attempt.
var vmNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// A catalog image id never contains a separator. An absolute path here is an
// arbitrary host file read, booted as a disk.
var imageIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var (
	indexNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	gitRefRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	paramNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	svcNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
)

// forbiddenPatchKeys are never accepted as tool input at any level. share
// grants an arbitrary host directory into a guest read-write.
var forbiddenPatchKeys = []string{"share", "image", "base", "iso", "console_password"}

func checkVMName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("vm name is required")
	}
	if name != strings.TrimSpace(name) {
		return "", fmt.Errorf("vm name %q has leading or trailing whitespace", name)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("vm name %q is a path traversal", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("vm name %q contains a path separator", name)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("vm name contains a null byte")
	}
	if !vmNameRE.MatchString(name) {
		return "", fmt.Errorf("invalid vm name %q: must match %s", name, vmNameRE)
	}
	return name, nil
}

func checkImageID(image string) (string, error) {
	if strings.TrimSpace(image) == "" {
		return "", fmt.Errorf("image id is required")
	}
	if strings.ContainsAny(image, `/\`) || strings.HasPrefix(image, "~") {
		return "", fmt.Errorf("image %q looks like a path; only catalog image ids are accepted, run list_images to see them", image)
	}
	if !imageIDRE.MatchString(image) {
		return "", fmt.Errorf("image id %q is not a valid catalog id", image)
	}
	return image, nil
}

// sharedDir is the one host directory an agent may read or write for a VM.
// It mirrors the layout of the writable 9p export.
func sharedDir(vm string) (string, error) {
	name, err := checkVMName(vm)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Join(config.Root(), "shared", name))
}

// checkHostPath confines a host path to ~/.stoat/shared/<vm>/. The order is
// the guard: resolve symlinks, then compare. A check before resolution is
// defeated by a symlink the guest creates inside its own mounted share.
// Comparison is by path element, so the sibling "work-evil" cannot pass as
// "work".
func checkHostPath(path, vm string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("path contains a null byte")
	}
	sandbox, err := sharedDir(vm)
	if err != nil {
		return "", err
	}
	// Expand ~ first. filepath.EvalSymlinks reads a literal "~" as a
	// relative directory name.
	candidate := config.Expand(path)
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("path %q must be absolute, under %s", path, sandbox)
	}
	resolved, err := resolveExisting(candidate)
	if err != nil {
		return "", err
	}
	if resolved != sandbox && !strings.HasPrefix(resolved, sandbox+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q resolves to %s, which is outside this VM's shared directory (%s)", path, resolved, sandbox)
	}
	return resolved, nil
}

// resolveExisting resolves every symlink in p. A destination that does not
// exist yet is legitimate for copy_from, so the deepest existing ancestor is
// resolved and the remaining elements are rejoined.
func resolveExisting(p string) (string, error) {
	rest := ""
	for cur := filepath.Clean(p); ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("path %q cannot be resolved", p)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// checkFlagFree refuses a value kong would read as a flag. forward and
// check_recipes splat their list arguments into argv as positionals, and
// forward(pairs=["--clear"]) once reached kong as --clear and wiped a VM's
// forwards.
func checkFlagFree(values []string, what string) error {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s contains an empty value", what)
		}
		if strings.HasPrefix(v, "-") {
			return fmt.Errorf("%s value %q may not start with a dash", what, v)
		}
	}
	return nil
}

// stripForbidden drops keys an agent may never set. It returns a new map:
// an agent that reads a VM back and passes it to update is doing something
// reasonable, and the field simply has no effect.
func stripForbidden(patch map[string]any) map[string]any {
	out := make(map[string]any, len(patch))
	for k, v := range patch {
		if !slices.Contains(forbiddenPatchKeys, k) {
			out[k] = v
		}
	}
	return out
}

// checkIndexName splits "<name>" or "<name>@<ref>" from the recipe index. A
// URL is refused here rather than in core: add_recipe is the tool an agent
// reaches, and a URL is a repository nobody curated.
func checkIndexName(ref string) (string, string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", "", fmt.Errorf("recipe name is required")
	}
	if strings.ContainsAny(ref, ":/\\") {
		return "", "", fmt.Errorf("invalid recipe name %q: index names only, not a URL", ref)
	}
	name, gitRef, hasRef := strings.Cut(ref, "@")
	if strings.Contains(gitRef, "@") {
		return "", "", fmt.Errorf("invalid recipe name %q: at most one @ref", ref)
	}
	if !indexNameRE.MatchString(name) {
		return "", "", fmt.Errorf("invalid recipe name %q: must match %s", name, indexNameRE)
	}
	if hasRef {
		if !gitRefRE.MatchString(gitRef) || strings.Contains(gitRef, "..") {
			return "", "", fmt.Errorf("invalid ref %q for recipe %q", gitRef, name)
		}
	}
	return name, gitRef, nil
}

func checkParamName(name string) (string, error) {
	if !paramNameRE.MatchString(name) {
		return "", fmt.Errorf("invalid param name %q: must match %s", name, paramNameRE)
	}
	return name, nil
}

// checkGuestPath requires an absolute guest path. A relative path is an
// error and is never resolved against $HOME, so one tool call means one
// thing on every guest.
func checkGuestPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("guest path is required")
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("guest path contains a null byte")
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("guest path %q must be absolute", path)
	}
	return path, nil
}

// checkSvcName bounds a service name. svc and svc_status render the guest
// file's template and pass the name as $1, so this is depth rather than the
// boundary.
func checkSvcName(name string) (string, error) {
	if !svcNameRE.MatchString(name) {
		return "", fmt.Errorf("invalid service name %q: must match %s", name, svcNameRE)
	}
	return name, nil
}

// requireRunning gates a tool whose description promises it refuses on a
// stopped VM. Without it, sshx.Run against a stopped VM's forwarded port
// surfaces ssh's own connection-refused exit rather than this error.
func requireRunning(v *config.VM) error {
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", qemu.ErrNotRunning, v.Name)
	}
	return nil
}
