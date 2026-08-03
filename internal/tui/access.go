package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/sshx"
)

// The access box sits beside the VM list and answers "how do I get into this
// one" without a trip to the detail screen. It exists because the answer is
// genuinely different per VM — root with a key on Alpine live, stoat with a
// key over ssh and a password at the console on cloud images, and whatever
// you typed into the installer on a disk VM — and guessing wrong leaves you
// staring at a login prompt.

// accessWidth is the box's content width, sized so the lines it holds do not
// wrap: a label column plus "stoat@127.0.0.1:65535" or a shortened key path.
// Values longer than the remaining room are truncated rather than wrapped —
// a wrapped path splits mid-token and reads as two broken lines.
const accessWidth = 40

// accessValueWidth is what is left for a value after the label column.
const accessValueWidth = accessWidth - fieldValueColumn

// accessMinTerminal is the terminal width below which the box is dropped
// entirely rather than squeezed: the list pane is listWidth wide and both
// carry a frame, so under this they would overlap or wrap.
const accessMinTerminal = listWidth + accessWidth + 4*paneBorderAndPadding

// paneBorderAndPadding is one side's border plus padding from paneStyle.
const paneBorderAndPadding = 3

// accessBox renders the access panel for v, or "" if there is nothing useful
// to say (no VM selected).
func accessBox(v *config.VM, cloudInit string, ciProg progress.Model, width int) string {
	if v == nil {
		return ""
	}

	f := fields{width: accessWidth}
	line := func(k, val string) { f.row("", k, val) }
	// truncated is for values that carry no styling and may be long.
	truncated := func(k, val string) {
		line(k, ansi.Truncate(val, accessValueWidth, "…"))
	}

	user := sshx.User(v)
	line("ssh", fmt.Sprintf("%s@127.0.0.1:%d", user, v.SSHPort))
	truncated("key", shortenPath(keys.PrivatePath()))

	switch {
	case v.ConsolePassword != "":
		line("console", user+" / "+v.ConsolePassword)
	case v.Mode == "live":
		// The apkovl leaves root with no password at all, which is why a live
		// VM's console drops straight into a shell.
		line("console", "root"+dimStyle.Render("  (no password)"))
	case v.Mode == "cloud":
		line("console", warnStyle.Render("locked — ssh only"))
	default:
		line("console", dimStyle.Render("set during install"))
	}

	body := ""
	if v.Mode == "cloud" {
		if l := cloudInitLabel(cloudInit); l != "" {
			line("setup", l)
			// The bar sits in the value column under the word it belongs to,
			// as its own label-less row.
			f.row("", "", cloudInitBar(ciProg, cloudInit))
		}
		body = "\n\n" + dimStyle.Render("password is console-only;\nssh is key-only")
	}

	return paneAt("access", f.String()+body, accessWidth, width)
}

// shortenPath swaps the home directory for "~" so a key path fits the box.
func shortenPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// joinAccess places the access box to the right of the list pane, or returns
// the pane unchanged when the terminal is too narrow to hold both. Top-
// aligned so the two boxes share a top edge regardless of height.
func joinAccess(pane, access string, termWidth int) string {
	if access == "" || termWidth < accessMinTerminal {
		return pane
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, pane, "  ", access)
}
