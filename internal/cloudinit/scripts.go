package cloudinit

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/guest"
)

// scriptDir is where WrapScripts places each script inside the guest.
const scriptDir = "/var/lib/stoat/recipes"

// MarkerDir holds one empty file per recipe that ran successfully at first
// boot. core.Apply reads it over ssh to rebuild v.Applied for a cloudinit VM,
// which never populated it at create time. The name is the recipe's, with no
// extension.
const MarkerDir = "/var/lib/stoat/.applied"

// SecretsEnvPath is the transient guest path used for cloud-init recipe
// secrets. It is written mode 0600 and removed after all recipe commands run.
const SecretsEnvPath = "/run/stoat/secrets.env"

// Script pairs a recipe's Name with the body WrapScripts should run for it,
// i.e. the manifest's Name and manifest.ScriptContent(osName) for the guest
// being provisioned.
type Script struct {
	Name    string
	Content string
	Env     []string
	Secrets map[string]string
}

// WrapScripts renders scripts into a #cloud-config fragment: each script's
// content becomes a write_files entry at scriptDir/<name>.sh with executable
// permissions, and a runcmd entry that executes it. Order is preserved in
// both write_files and runcmd: provision-stage scripts must run in
// selection order.
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
	hasSecrets := false
	for _, s := range scripts {
		if len(s.Secrets) > 0 {
			hasSecrets = true
			break
		}
	}
	if hasSecrets {
		fmt.Fprintf(&wf, "  - path: %s\n", SecretsEnvPath)
		wf.WriteString("    permissions: '0600'\n")
		wf.WriteString("    content: |\n")
		wf.WriteString(indentBlock(secretEnv(scripts)))
	}
	if prelude != "" {
		setup := "sh -c " + guest.ShQuote(prelude+"stoat_pkg_setup")
		// YAML plain scalars cannot contain the unindented newlines in a
		// guest prelude. Encode the complete shell command as a YAML string;
		// the parser restores those newlines before cloud-init invokes sh.
		rc.WriteString(fmt.Sprintf("  - %s\n", strconv.Quote(setup)))
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
		command := recipeCommand(s, path, marker)
		if len(s.Env) > 0 || len(s.Secrets) > 0 {
			command = strconv.Quote(command)
		}
		fmt.Fprintf(&rc, "  - %s\n", command)
	}
	if hasSecrets {
		fmt.Fprintf(&rc, "  - rm -f %s\n", SecretsEnvPath)
	}

	return "#cloud-config\n" + wf.String() + rc.String()
}

func secretEnv(scripts []Script) string {
	var b strings.Builder
	for _, s := range scripts {
		names := make([]string, 0, len(s.Secrets))
		for name := range s.Secrets {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			key := namespacedSecret(s.Name, name)
			fmt.Fprintf(&b, "%s=%s\n", key, guest.ShQuote(s.Secrets[name]))
		}
	}
	return b.String()
}

func namespacedSecret(recipe, param string) string {
	if plainNamespacePart(recipe) && plainNamespacePart(param) {
		return "STOAT_PARAM_" + strings.ToUpper(recipe) + "_" + strings.ToUpper(param)
	}
	return "STOAT_PARAM_X" + hex.EncodeToString([]byte(recipe)) + "_" + hex.EncodeToString([]byte(param))
}

// plainNamespacePart retains the historical readable spelling for the
// ordinary lower-case recipe names and parameter names already in use. Any
// punctuation, underscore, or uppercase uses the pair encoding above, which
// makes the recipe/param boundary unambiguous and preserves case identity.
func plainNamespacePart(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func recipeCommand(s Script, path, marker string) string {
	var b strings.Builder
	if len(s.Secrets) > 0 {
		fmt.Fprintf(&b, ". %s && ", SecretsEnvPath)
	}
	for _, entry := range s.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s && ", key, guest.ShQuote(value))
	}
	names := make([]string, 0, len(s.Secrets))
	for name := range s.Secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key := "STOAT_PARAM_" + strings.ToUpper(name)
		fmt.Fprintf(&b, "export %s=\"$%s\" && ", key, namespacedSecret(s.Name, name))
	}
	output := "/tmp/.stoat-out/" + s.Name
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "STOAT_OUTPUT=%s && export STOAT_OUTPUT && mkdir -p /tmp/.stoat-out && chmod 700 /tmp/.stoat-out && : > \"$STOAT_OUTPUT\" && ", guest.ShQuote(output))
	}
	fmt.Fprintf(&b, "%s && mkdir -p %s && ", path, MarkerDir)
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "if [ -f %s ]; then cp %s %s.out; fi && ", output, output, marker)
	}
	fmt.Fprintf(&b, "touch %s", marker)
	return b.String()
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
