package tui

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/recipes"
)

// openRecipesDir hands the recipes directory to $EDITOR, the same escape
// hatch "E" uses for a vm.toml.
//
// Authoring a recipe has always been "put a correctly named file in this
// directory" — recipes.Install never overwrites, so an edit survives
// upgrades, and recipes.List picks up a new file with no code change at all.
// The only real problem was that nothing in the UI ever said so. A key that
// puts you in the directory says it better than any amount of documentation.
func openRecipesDir() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, recipes.Dir())
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg("editor: " + err.Error())
		}
		// The form reads the recipe list when it opens, so a recipe added
		// just now shows up on the next "n" with nothing else to do.
		return statusMsg("recipes: " + recipes.Dir())
	})
}
