package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runRecipeShow prints one recipe's contract without filtering it by a VM's
// operating system. A caller reads this before choosing an apply target.
func runRecipeShow(a *Args, stdout, stderr io.Writer) int {
	r, err := core.RecipeShow(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeShowResult{Recipe: wire.FromRecipeSchema(r)})
	}

	fmt.Fprintf(stdout, "%s: %s\n", r.Name, r.Description)
	fmt.Fprintf(stdout, "schema: %d\nruntime: %s\n", r.Schema, r.Runtime)
	if len(r.Params) > 0 {
		fmt.Fprintln(stdout, "\nparams:")
		for _, p := range r.Params {
			detail := p.Type
			switch {
			case p.Type == "enum":
				detail = "enum(" + strings.Join(p.Values, ", ") + ")"
			case p.Required:
				detail += ", required"
			case p.Default != "":
				detail += ", default " + p.Default
			}
			fmt.Fprintf(stdout, "  %-14s %-28s %s\n", p.Name, detail, p.Help)
		}
	}
	if len(r.Outputs) > 0 {
		fmt.Fprintln(stdout, "\noutputs:")
		for _, o := range r.Outputs {
			fmt.Fprintf(stdout, "  %-14s %s\n", o.Name, o.Help)
		}
	}
	if r.Health != nil {
		fmt.Fprintf(stdout, "\nhealth: %s (timeout %s)\n", r.Health.Check, r.Health.Timeout)
	}
	return ExitOK
}
