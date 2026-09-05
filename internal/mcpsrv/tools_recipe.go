package mcpsrv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

type addRecipeIn struct {
	Name string `json:"name" jsonschema:"recipe name from the index, optionally with @ref such as tailscale@v1.2; a URL is refused"`
	Ref  string `json:"ref,omitempty" jsonschema:"tag or branch to pin, when it is not written into name"`
}

type recipeNameIn struct {
	Name string `json:"name,omitempty" jsonschema:"recipe name; omit to update every remote recipe"`
}

type removeRecipeIn struct {
	Name string `json:"name" jsonschema:"recipe name to remove"`
}

func (s *srv) registerRecipe(server *mcp.Server) {
	register(server, "add_recipe", classMutate,
		"Install a recipe from the curated index, pinned at a tag or branch. Only index names are accepted; a git URL is refused, because a URL is a repository nobody curated. It clones and validates the recipe and writes a lock entry. It runs no guest code, so it needs no agent access. Mutating: it writes to the recipe cache and the lockfile.",
		func(ctx context.Context, in addRecipeIn) (wire.RecipeCatalog, error) {
			name, ref, err := checkIndexName(in.Name)
			if err != nil {
				return wire.RecipeCatalog{}, err
			}
			if in.Ref != "" {
				_, r, err := checkIndexName(name + "@" + in.Ref)
				if err != nil {
					return wire.RecipeCatalog{}, err
				}
				ref = r
			}
			if err := core.AddRecipe(name, core.AddOpts{Ref: ref, Yes: true}); err != nil {
				return wire.RecipeCatalog{}, err
			}
			return currentRecipes()
		})

	register(server, "update_recipe", classMutate,
		"Re-resolve a remote recipe's tag or branch, check the new commit out, and rewrite the lock entry. With no name it updates every remote recipe. It refuses a recipe directory with uncommitted changes. It runs no guest code. Mutating: it changes the recipe cache and the lockfile.",
		func(ctx context.Context, in recipeNameIn) (wire.RecipeCatalog, error) {
			name := in.Name
			if name != "" {
				n, _, err := checkIndexName(name)
				if err != nil {
					return wire.RecipeCatalog{}, err
				}
				name = n
			}
			if err := core.UpdateRecipe(name); err != nil {
				return wire.RecipeCatalog{}, err
			}
			return currentRecipes()
		})

	register(server, "remove_recipe", classMutate,
		"Remove a remote recipe: its declaration, its lock entry and its directory. It refuses while any VM lists that recipe. There is no force option on this tool; a person removes a recipe a VM still uses, from the CLI. Mutating and not reversible from here, though add_recipe reinstalls it.",
		func(ctx context.Context, in removeRecipeIn) (wire.RecipeCatalog, error) {
			name, _, err := checkIndexName(in.Name)
			if err != nil {
				return wire.RecipeCatalog{}, err
			}
			// force is deliberately absent rather than false-by-default: a
			// parameter that exists is eventually reachable.
			if err := core.RemoveRecipe(name, false); err != nil {
				return wire.RecipeCatalog{}, err
			}
			return currentRecipes()
		})
}

func currentRecipes() (wire.RecipeCatalog, error) {
	rs, err := core.Recipes(core.RecipeFilter{})
	if err != nil {
		return wire.RecipeCatalog{}, err
	}
	return wire.RecipeCatalog{Recipes: wire.FromRecipes(rs)}, nil
}
