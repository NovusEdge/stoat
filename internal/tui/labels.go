package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// This file turns stoat's internal identifiers into the words a user reads.
// Recipes are stored as filenames. Modes are the values qemu branches on.
// Neither is something the user picked. Showing them raw made the UI read
// like a config file.

// recipeLabel is the display name for a recipe file. Recipes live on disk as
// "<name>.<os>.sh" or "<name>.cloud.yaml". The suffix names the OS and
// backend the file targets. The picker has already filtered on that suffix,
// so repeating it in every row is noise. "xfce.alpine.sh" reads as "xfce".
//
// Only the display changes. VM.Recipes still stores the filename, since
// recipes.Read opens it by that name. See the recipe-naming decision in
// CHECKPOINT.md, where storing bare names was considered and rejected.
func recipeLabel(file string) string {
	name := strings.TrimSuffix(file, ".yaml")
	name = strings.TrimSuffix(name, ".sh")
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// modeLabel is what a mode row shows: the value plus the one fact about it a
// user needs before choosing. Without that fact, "disk" gives no warning
// that the OS must be installed by hand, and a user ends up with a VM they
// cannot provision and no idea why.
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

// cycle returns the choice after (d=1) or before (d=-1) cur in choices,
// wrapping at either end. An unrecognised cur is treated as choices[0]
// before stepping.
func cycle(choices []string, cur string, d int) string {
	idx := 0
	for i, c := range choices {
		if c == cur {
			idx = i
		}
	}
	return choices[(idx+d+len(choices))%len(choices)]
}

// displayChoices is the fixed cycle the create form, the edit form, and the
// detail screen's "d" key all offer for config.VM.Display.
var displayChoices = []string{"auto", "window", "vnc"}

// displayPrefLabel is what a display preference row shows: "" and "auto" both
// read as "auto", since config.VM.Display treats them the same.
func displayPrefLabel(pref string) string {
	if pref == "" {
		return "auto"
	}
	return pref
}

// nextDisplayPref cycles a display preference forward through
// displayChoices: auto -> window -> vnc -> auto. An unrecognised value (a
// hand-edited vm.toml) is treated as auto, the same fallback validateDisplay
// applies to "".
func nextDisplayPref(pref string) string {
	cur := displayPrefLabel(pref)
	for i, c := range displayChoices {
		if c == cur {
			return displayChoices[(i+1)%len(displayChoices)]
		}
	}
	return displayChoices[0]
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

// wrapItems lays out picker items across as many lines as they need, and
// keeps continuation lines aligned under the first. Width is measured with
// lipgloss, since the item under the cursor carries styling, and counting
// its escape bytes would wrap far too early.
//
// The recipes row is inline and grows with the recipe catalog. At four
// entries it ran past the pane and wrapped mid-item, leaving a bare "xfce"
// on its own line under the label column.
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
