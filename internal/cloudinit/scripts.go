package cloudinit

import (
	"fmt"
	"strings"

	"github.com/novusedge/stoat/internal/guest"
)

// scriptDir is where WrapScripts places each script inside the guest,
// matching docs/recipe-spec-v2.md's cloudinit execution model.
const scriptDir = "/var/lib/stoat/recipes"

// MarkerDir holds one empty file per recipe that ran successfully at first
// boot. core.Apply reads it over ssh to rebuild v.Applied for a cloudinit VM,
// which never populated it at create time. The name is the recipe's, with no
// extension.
const MarkerDir = "/var/lib/stoat/.applied"

// Script pairs a recipe's Name with the body WrapScripts should run for it,
// i.e. the manifest's Name and manifest.ScriptContent(osName) for the guest
// being provisioned.
type Script struct {
	Name    string
	Content string
}

// WrapScripts renders scripts into a #cloud-config fragment: each script's
// content becomes a write_files entry at scriptDir/<name>.sh with executable
// permissions, and a runcmd entry that executes it. Order is preserved in
// both write_files and runcmd: provision-stage scripts must run in
// selection order (docs/recipe-spec-v2.md, "For cloudinit backend").
//
// The returned fragment is a plain string, not yet merge_how-tagged. It is
// meant to be handed to Seed alongside other recipe bodies, like any other
// cloud recipe fragment (see userData in cloudinit.go). It goes through
// withMergeHow and buildArchive the same way every other document does.
// prelude is prepended to each script's content and, when non-empty, run
// once via stoat_pkg_setup as the first runcmd entry, so the package index
// is refreshed before any script that installs a package runs.
func WrapScripts(scripts []Script, prelude string) string {
	if len(scripts) == 0 {
		return ""
	}

	var wf, rc strings.Builder
	wf.WriteString("write_files:\n")
	rc.WriteString("runcmd:\n")
	if prelude != "" {
		rc.WriteString(fmt.Sprintf("  - sh -c %s\n", guest.ShQuote(prelude+"stoat_pkg_setup")))
	}
	for _, s := range scripts {
		path := fmt.Sprintf("%s/%s.sh", scriptDir, s.Name)
		fmt.Fprintf(&wf, "  - path: %s\n", path)
		wf.WriteString("    permissions: '0755'\n")
		wf.WriteString("    content: |\n")
		wf.WriteString(indentBlock(guest.WithPrelude(s.Content, prelude)))
		// The script runs, then drops a marker on success. core.Apply reads
		// MarkerDir over ssh to rebuild v.Applied post-boot. The && chain
		// leaves no marker for a script that failed, so a failed recipe stays
		// pending instead of being recorded as applied.
		marker := fmt.Sprintf("%s/%s", MarkerDir, s.Name)
		fmt.Fprintf(&rc, "  - %s && mkdir -p %s && touch %s\n", path, MarkerDir, marker)
	}

	return "#cloud-config\n" + wf.String() + rc.String()
}

// indentBlock indents body by six spaces, the depth a YAML block scalar
// needs under write_files' "content: |": two for the write_files list item,
// two more for content: under path/permissions, two more for the scalar
// itself. It always ends in a newline, so the next YAML key starts flush
// left even when body does not already end in one.
func indentBlock(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		if l == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("      " + l + "\n")
	}
	return b.String()
}
