package guest

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// ShQuote wraps s in single quotes for POSIX sh, escaping embedded quotes.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteArgv(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShQuote(a)
	}
	return strings.Join(q, " ")
}

// shTemplate renders a [svc] or [cmd] template into a function body:
// {name} becomes "$1"; a template without it gets "$@" appended.
func shTemplate(tmpl string) string {
	if strings.Contains(tmpl, "{name}") {
		return strings.ReplaceAll(tmpl, "{name}", `"$1"`)
	}
	return tmpl + ` "$@"`
}

// pkgMgr is the basename of pkg.install's first word.
func pkgMgr(o OS) string {
	if len(o.Pkg.Install) == 0 {
		return ""
	}
	return path.Base(o.Pkg.Install[0])
}

// verbs lists every function the prelude defines, in a fixed order, so the
// sh and python renderings stay in step and the golden files are stable.
func verbs(o OS) []struct{ name, tmpl string } {
	v := []struct{ name, tmpl string }{
		{"stoat_svc_enable", o.Svc.Enable},
		{"stoat_svc_start", o.Svc.Start},
		{"stoat_svc_stop", o.Svc.Stop},
		{"stoat_svc_restart", o.Svc.Restart},
		{"stoat_svc_status", o.Svc.Status},
	}
	names := make([]string, 0, len(o.Cmd))
	for n := range o.Cmd {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v = append(v, struct{ name, tmpl string }{"stoat_" + n, o.Cmd[n]})
	}
	return v
}

// Prelude renders the verb definitions and STOAT_* variables for runtime
// ("sh" or "python3"). sshx and cloudinit put it in front of every recipe
// body, so a recipe behaves the same over ssh and in runcmd.
func Prelude(o OS, runtime string) string {
	if runtime == "python3" {
		return preludePython(o)
	}
	var b strings.Builder
	setup := o.Pkg.Setup
	if strings.TrimSpace(setup) == "" {
		setup = ":"
	}
	fmt.Fprintf(&b, "stoat_pkg_setup() { %s; }\n", setup)
	fmt.Fprintf(&b, "stoat_pkg_install() { %s \"$@\"; }\n", quoteArgv(o.Pkg.Install))
	for _, v := range verbs(o) {
		fmt.Fprintf(&b, "%s() { %s; }\n", v.name, shTemplate(v.tmpl))
	}
	for _, k := range sortedKeys(o.Pkg.Env) {
		fmt.Fprintf(&b, "export %s=%s\n", k, ShQuote(o.Pkg.Env[k]))
	}
	fmt.Fprintf(&b, "STOAT_OS=%s; STOAT_INIT=%s; STOAT_PKGMGR=%s\n", o.Name, o.Init, pkgMgr(o))
	b.WriteString("export STOAT_OS STOAT_INIT STOAT_PKGMGR\n")
	return b.String()
}

func preludePython(o OS) string {
	var b strings.Builder
	b.WriteString("import os, subprocess\n")
	fmt.Fprintf(&b, "os.environ[\"STOAT_OS\"] = %q\nos.environ[\"STOAT_INIT\"] = %q\nos.environ[\"STOAT_PKGMGR\"] = %q\n", o.Name, string(o.Init), pkgMgr(o))
	for _, k := range sortedKeys(o.Pkg.Env) {
		fmt.Fprintf(&b, "os.environ[%q] = %q\n", k, o.Pkg.Env[k])
	}
	b.WriteString("def _run(*argv): subprocess.run(list(argv), check=True)\n")
	setup := o.Pkg.Setup
	if strings.TrimSpace(setup) == "" {
		setup = ":"
	}
	fmt.Fprintf(&b, "def stoat_pkg_setup(): _run(\"sh\", \"-c\", %q)\n", setup)
	fmt.Fprintf(&b, "def stoat_pkg_install(*pkgs): _run(%s, *pkgs)\n", pyArgv(o.Pkg.Install))
	for _, v := range verbs(o) {
		fmt.Fprintf(&b, "def %s(*args): _run(\"sh\", \"-c\", '%s', \"stoat\", *args)\n", v.name, shTemplate(v.tmpl))
	}
	return b.String()
}

func pyArgv(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = fmt.Sprintf("%q", a)
	}
	return strings.Join(q, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WithPrelude inserts prelude after a leading "#!" line, or in front of the
// body when there is none. cloud-init's write_files keeps the shebang
// first; a prelude before it would make the file run under /bin/sh with the
// shebang as a comment, which changes nothing on Linux but reads wrong.
func WithPrelude(body, prelude string) string {
	if strings.HasPrefix(body, "#!") {
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			return body[:i+1] + prelude + body[i+1:]
		}
		return body + "\n" + prelude
	}
	return prelude + body
}
