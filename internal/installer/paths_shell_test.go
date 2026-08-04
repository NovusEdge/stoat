package installer

import (
	"os/exec"
	"strings"
	"testing"
)

// TestWrapRCLineAgainstRealShells checks WrapRCLine's output the only way that
// really settles it: by pasting it into actual shells and asking what PATH they
// ended up with. TestWrapRCLineReassembles covers the same property with a
// hand-written unquoter, which runs anywhere but can only ever be as right as
// our own understanding of shell quoting; this one is ground truth.
//
// The dirs below deliberately include a quote, a backslash, a multi-byte rune
// and shell metacharacters, at widths that force breaks in different places:
// a break that lands mid-escape, or that leaves dir's quoting open, shows up
// here as a wrong PATH rather than as a plausible-looking string.
func TestWrapRCLineAgainstRealShells(t *testing.T) {
	dirs := []string{
		"/home/exampleuser/" + strings.Repeat("verylongsegment/", 9) + "bin",
		"/home/user/it's/a/path/" + strings.Repeat("x", 60),
		`/home/user/back\slash/and 'quote'/` + strings.Repeat("y", 50),
		"/home/user/ünicöde/pathß/" + strings.Repeat("z", 60),
		"/home/user/$(echo sub)/`echo tick`/${HOME}/" + strings.Repeat("w", 40),
		"/tmp/short",
	}
	widths := []int{10, 15, 20, 30, 45, 60, 80, 200}

	for _, name := range []string{"sh", "bash", "zsh"} {
		sh, err := exec.LookPath(name)
		if err != nil {
			// Same reason the qemu-img and xorriso tests skip: a missing host
			// tool must not turn CI red for a thing it cannot check.
			t.Logf("skipping %s: not installed", name)
			continue
		}
		for _, dir := range dirs {
			for _, w := range widths {
				lines := WrapRCLine("/bin/bash", dir, w)
				// PATH=ORIG first, so the assertion also proves the ":$PATH"
				// suffix survived: that is the exact tail the original bug ate.
				script := "PATH=ORIG\n" + strings.Join(lines, "\n") + "\nprintf '%s' \"$PATH\"\n"
				out, err := exec.Command(sh, "-c", script).Output()
				if err != nil {
					t.Fatalf("%s w=%d dir=%q: %v\nscript:\n%s", name, w, dir, err, script)
				}
				if want := dir + ":ORIG"; string(out) != want {
					t.Errorf("%s w=%d dir=%q:\n got %q\nwant %q\nscript:\n%s", name, w, dir, out, want, script)
				}
				if len(lines) > 1 {
					for _, l := range lines {
						if len(l) > w {
							t.Errorf("%s w=%d: wrapped line is %d bytes, wider than the terminal: %q", name, w, len(l), l)
						}
					}
				}
			}
		}
	}
}
