package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/project"
	"github.com/novusedge/stoat/internal/recipes"
)

// Shell completion. The scripts below only forward the words typed so far to
// the hidden `stoat __complete`, which walks the kong model and answers. Every
// rule lives in Go, where a test can run it; the three shells differ only in
// how they collect words and read the answer.
//
// kong has no completion support of its own. A grammar field opts into
// dynamic values with a `complete:"vm|image|recipe"` tag on the flag or
// positional.

const bashCompletion = `_stoat() {
    local IFS=$'\n'
    COMPREPLY=( $(stoat __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null) )
}
complete -F _stoat stoat
`

const zshCompletion = `#compdef stoat
_stoat() {
    local -a candidates
    candidates=(${(f)"$(stoat __complete "${(@)words[2,CURRENT]}" 2>/dev/null)"})
    compadd -a candidates
}
if [ "$funcstack[1]" = "_stoat" ]; then
    _stoat "$@"
else
    compdef _stoat stoat
fi
`

const fishCompletion = `function __stoat_complete
    set -l words (commandline -opc) (commandline -ct)
    set -e words[1]
    stoat __complete $words 2>/dev/null
end
complete -c stoat -f -a '(__stoat_complete)'
`

var completionScripts = map[string]string{
	"bash": bashCompletion,
	"zsh":  zshCompletion,
	"fish": fishCompletion,
}

func runCompletion(a *Args, stdout io.Writer) int {
	_, err := io.WriteString(stdout, completionScripts[a.Sub])
	if err != nil {
		return ExitFail
	}
	return ExitOK
}

func runComplete(a *Args, stdout io.Writer) int {
	for _, c := range complete(a.Command) {
		fmt.Fprintln(stdout, c)
	}
	return ExitOK
}

// complete returns the candidates for the last element of words, which is the
// word under the cursor and may be empty. The elements before it are what
// the user has already typed, without the program name.
func complete(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	var g grammar
	var help bytes.Buffer
	p, err := newParser(&g, &help)
	if err != nil {
		return nil
	}
	prefix, done := words[len(words)-1], words[:len(words)-1]

	node, pos := p.Model.Node, 0
	var pending *kong.Flag
	for _, w := range done {
		switch {
		case pending != nil && w == "=":
			// bash splits "--image=alp" into three words at the "=".
		case pending != nil:
			pending = nil
		case w == "--":
			return nil
		case strings.HasPrefix(w, "-"):
			name, _, hasValue := strings.Cut(w, "=")
			if f := findFlag(node, name); f != nil && !hasValue && !f.IsBool() && !f.IsCounter() {
				pending = f
			}
		default:
			if c := findChild(node, w); c != nil && pos == 0 {
				node = c
			} else {
				pos++
			}
		}
	}
	if prefix == "=" {
		prefix = ""
	}

	if pending != nil {
		return matching(flagValues(pending.Value), prefix)
	}
	// Everything from a passthrough positional on belongs to the guest.
	if len(node.Positional) > 0 && pos > 0 {
		if v := node.Positional[min(pos, len(node.Positional)-1)]; v.Tag != nil && v.Tag.Passthrough {
			return nil
		}
	}
	if strings.HasPrefix(prefix, "--") && strings.Contains(prefix, "=") {
		name, val, _ := strings.Cut(prefix, "=")
		f := findFlag(node, name)
		if f == nil {
			return nil
		}
		var out []string
		for _, v := range matching(flagValues(f.Value), val) {
			out = append(out, name+"="+v)
		}
		return out
	}
	if strings.HasPrefix(prefix, "-") {
		return matching(flagNames(node), prefix)
	}

	var out []string
	for _, c := range node.Children {
		if !c.Hidden {
			out = append(out, c.Name)
		}
	}
	if len(node.Positional) > 0 {
		v := node.Positional[min(pos, len(node.Positional)-1)]
		if pos < len(node.Positional) || v.IsSlice() {
			out = append(out, flagValues(v)...)
		}
	}
	return matching(out, prefix)
}

// findFlag looks name up on node and its ancestors, since kong lets a parent's
// flags appear after the subcommand.
func findFlag(node *kong.Node, name string) *kong.Flag {
	for n := node; n != nil; n = n.Parent {
		for _, f := range n.Flags {
			if name == "--"+f.Name || (f.Short != 0 && name == "-"+string(f.Short)) || slices.Contains(f.Aliases, strings.TrimPrefix(name, "--")) {
				return f
			}
		}
	}
	return nil
}

func findChild(node *kong.Node, name string) *kong.Node {
	for _, c := range node.Children {
		if c.Name == name || slices.Contains(c.Aliases, name) {
			return c
		}
	}
	return nil
}

// flagNames lists the visible long flags of node and its ancestors. --json is
// not in the grammar: Main strips it from argv before kong runs.
func flagNames(node *kong.Node) []string {
	out := []string{"--json"}
	for n := node; n != nil; n = n.Parent {
		for _, f := range n.Flags {
			if !f.Hidden {
				out = append(out, "--"+f.Name)
			}
		}
	}
	return out
}

// flagValues is the candidate list for a flag or positional: its enum, or the
// stoat state its complete tag names. A value with neither has no candidates.
func flagValues(v *kong.Value) []string {
	if v.Tag != nil && v.Tag.Passthrough {
		return nil
	}
	if v.Enum != "" {
		return v.EnumSlice()
	}
	if v.Tag == nil {
		return nil
	}
	switch v.Tag.Get("complete") {
	case "vm":
		return vmNames()
	case "image":
		return imageIDs()
	case "recipe":
		return recipeNames()
	}
	return nil
}

// vmNames is every VM in the data root plus the keys the current directory's
// stoat.toml declares, so `stoat up <TAB>` offers "dev" in a project as well
// as "myrepo-dev".
func vmNames() []string {
	var out []string
	if p, ok, _ := project.Find(); ok {
		for _, v := range p.VMs {
			out = append(out, v.Key)
		}
	}
	vms, _ := config.List()
	for _, v := range vms {
		out = append(out, v.Name)
	}
	return out
}

func imageIDs() []string {
	imgs, _ := core.Images()
	var out []string
	for _, i := range imgs {
		if i.ID != "" {
			out = append(out, i.ID)
		} else {
			out = append(out, i.File)
		}
	}
	return out
}

func recipeNames() []string {
	ms, _ := recipes.ListManifests()
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func matching(candidates []string, prefix string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
