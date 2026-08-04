package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// This file is the one place that turns stoat's internal identifiers into the
// words a user reads. Recipes are stored as filenames and modes as the values
// qemu branches on; neither is something the user picked, and showing them raw
// made the UI read like a config file.

// recipeLabel is the display name for a recipe file. Recipes live on disk as
// "<name>.<os>.sh" or "<name>.cloud.yaml". The suffix says which OS and which
// backend the file targets, which the picker has already filtered on, so
// repeating it in every row is noise. "xfce.alpine.sh" reads as "xfce".
//
// Only the display changes: VM.Recipes still stores the filename, because
// that is what recipes.Read opens. (See the recipe-naming decision in
// CHECKPOINT.md, where storing bare names was considered and rejected.)
func recipeLabel(file string) string {
	name := strings.TrimSuffix(file, ".yaml")
	name = strings.TrimSuffix(name, ".sh")
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// modeLabel is what a mode row shows: the value plus the one thing about it a
// user needs to know before choosing. "disk" not saying "you have to install
// an OS yourself first" is exactly how someone ends up with a VM that can't
// be provisioned and no idea why.
func modeLabel(mode string) string {
	switch mode {
	case "live":
		return "live"
	case "disk":
		return "disk"
	case "cloud":
		return "cloud"
	}
	return mode
}

// modeHint is the sentence shown under a mode picker for the selected mode.
func modeHint(mode string) string {
	switch mode {
	case "live":
		return "runs in RAM · ssh works now · reboot wipes it"
	case "disk":
		return "you install the OS at the console first"
	case "cloud":
		return "prebuilt image · ssh + recipes at first boot"
	}
	return ""
}

// wrapItems lays out picker items across as many lines as they need, keeping
// continuation lines aligned under the first. Width is measured with lipgloss
// because the item under the cursor carries styling, and counting its escape
// bytes would wrap far too early.
//
// It exists because the recipes row is inline and grows with the recipe
// catalog: at four entries it ran past the pane and wrapped mid-item, which
// put a bare "xfce" on its own line under the label column.
func wrapItems(items []string, width int, indent string) string {
	if len(items) == 0 {
		return ""
	}
	const gap = "  "
	var out strings.Builder
	line := ""
	for _, it := range items {
		candidate := it
		if line != "" {
			candidate = line + gap + it
		}
		if line != "" && lipgloss.Width(candidate) > width {
			out.WriteString(line + "\n" + indent)
			line = it
			continue
		}
		line = candidate
	}
	out.WriteString(line)
	return out.String()
}
