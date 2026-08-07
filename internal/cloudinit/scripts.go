package cloudinit

import (
	"fmt"
	"strings"
)

// scriptDir is where WrapScripts places each script inside the guest,
// matching docs/recipe-spec-v2.md's cloudinit execution model.
const scriptDir = "/var/lib/stoat/recipes"

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
func WrapScripts(scripts []Script) string {
	if len(scripts) == 0 {
		return ""
	}

	var wf, rc strings.Builder
	wf.WriteString("write_files:\n")
	rc.WriteString("runcmd:\n")
	for _, s := range scripts {
		path := fmt.Sprintf("%s/%s.sh", scriptDir, s.Name)
		wf.WriteString(fmt.Sprintf("  - path: %s\n", path))
		wf.WriteString("    permissions: '0755'\n")
		wf.WriteString("    content: |\n")
		wf.WriteString(indentBlock(s.Content))
		rc.WriteString(fmt.Sprintf("  - %s\n", path))
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
