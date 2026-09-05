package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/recipes"
)

// paramForm is the schema-driven form shown after a recipe with parameters is
// selected. Pointer-backed values let huh fields and Spec projection share one
// source without reaching into huh's private selector state.
type paramForm struct {
	recipe   string
	form     *huh.Form
	values   map[string]*string
	bools    map[string]*bool
	defaults map[string]string
	params   []core.RecipeParam
}

func newParamForm(recipe core.Recipe) *paramForm {
	p := &paramForm{
		recipe:   recipe.Name,
		values:   map[string]*string{},
		bools:    map[string]*bool{},
		defaults: map[string]string{},
		params:   append([]core.RecipeParam{}, recipe.Params...),
	}
	fields := make([]huh.Field, 0, len(recipe.Params))
	for _, param := range recipe.Params {
		p.defaults[param.Name] = param.Default
		switch param.Type {
		case "bool":
			value := new(bool)
			*value = param.Default == "true"
			p.bools[param.Name] = value
			fields = append(fields, huh.NewConfirm().Title(param.Name).Description(param.Help).Value(value).Affirmative("true").Negative("false"))
		case "enum":
			value := new(string)
			*value = param.Default
			p.values[param.Name] = value
			fields = append(fields, huh.NewSelect[string]().Title(param.Name).Description(param.Help).Options(huh.NewOptions(param.Values...)...).Value(value))
		default:
			value := new(string)
			*value = param.Default
			p.values[param.Name] = value
			input := huh.NewInput().Title(param.Name).Description(param.Help).Value(value).Validate(paramValidator(param))
			if param.Type == "secret" {
				input.EchoMode(huh.EchoModePassword)
			}
			fields = append(fields, input)
		}
	}
	p.form = huh.NewForm(huh.NewGroup(fields...)).WithAccessible(false).WithWidth(formContentWidth - 2)
	return p
}

func paramValidator(param core.RecipeParam) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			if param.Required {
				return fmt.Errorf("%s is required", param.Name)
			}
			return nil
		}
		switch param.Type {
		case "int":
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("%s must be an integer", param.Name)
			}
		case "bool":
			if value != "true" && value != "false" {
				return fmt.Errorf("%s must be true or false", param.Name)
			}
		case "enum":
			for _, choice := range param.Values {
				if choice == value {
					return nil
				}
			}
			return fmt.Errorf("%s must be one of %s", param.Name, strings.Join(param.Values, ", "))
		}
		return nil
	}
}

func (p *paramForm) init() tea.Cmd {
	return p.form.Init()
}

func (p *paramForm) valuesSnapshot() map[string]string {
	out := make(map[string]string, len(p.params))
	for _, param := range p.params {
		if param.Type == "bool" {
			out[param.Name] = strconv.FormatBool(*p.bools[param.Name])
		} else {
			out[param.Name] = *p.values[param.Name]
		}
	}
	return out
}

// parameterForms queues dependency forms before the explicitly selected
// recipe. Dependencies are real selections, so their required inputs must be
// completed through the same wizard rather than being left for core.Plan to
// reject after the user submits the VM form.
func (f *formModel) parameterForms(root string, added []core.DepAddition) ([]*paramForm, error) {
	names := make([]string, 0, len(added)+1)
	seen := make(map[string]bool, len(added)+1)
	for _, addition := range added {
		if !seen[addition.Recipe] {
			names = append(names, addition.Recipe)
			seen[addition.Recipe] = true
		}
	}
	if !seen[root] {
		names = append(names, root)
	}
	forms := make([]*paramForm, 0, len(names))
	for _, name := range names {
		recipe, err := core.RecipeShow(name)
		if err != nil {
			return nil, err
		}
		if manifest, ok, err := recipes.ManifestFor(name); err != nil {
			return nil, err
		} else if ok {
			byName := make(map[string]core.RecipeParam, len(recipe.Params))
			for _, param := range recipe.Params {
				byName[param.Name] = param
			}
			ordered := make([]core.RecipeParam, 0, len(recipe.Params))
			for _, param := range manifest.OrderedParams() {
				if projected, exists := byName[param.Name]; exists {
					ordered = append(ordered, projected)
					delete(byName, param.Name)
				}
			}
			for _, param := range recipe.Params {
				if _, exists := byName[param.Name]; exists {
					ordered = append(ordered, param)
					delete(byName, param.Name)
				}
			}
			recipe.Params = ordered
		}
		if len(recipe.Params) > 0 {
			forms = append(forms, newParamForm(recipe))
		}
	}
	return forms, nil
}

func (f *formModel) cancelParamForms() {
	root := f.paramRoot
	f.paramForm = nil
	f.paramQueue = nil
	f.paramRoot = ""
	if root != "" {
		f.recipeExplicit[root] = false
		delete(f.paramValues, root)
	}
	f.recomputeRecipeSelection()
	f.cleanupParamValues()
}

func (f *formModel) cleanupParamValues() {
	for name := range f.paramValues {
		if !f.recipeSel[name] {
			delete(f.paramValues, name)
		}
	}
}

func (f *formModel) recomputeRecipeSelection() {
	if f.recipeSel == nil {
		f.recipeSel = map[string]bool{}
	}
	for name := range f.recipeSel {
		f.recipeSel[name] = false
	}
	roots := make([]string, 0)
	for _, name := range f.recipeNames {
		if f.recipeExplicit[name] {
			roots = append(roots, name)
			f.recipeSel[name] = true
		}
	}
	added, err := resolveDeps(f.resolvedOS(), roots)
	if err != nil {
		return
	}
	for _, dep := range added {
		f.recipeSel[dep.Recipe] = true
	}
	f.cleanupParamValues()
}

func (m model) viewParamForm() string {
	body := m.form.paramForm.form.View()
	box := paneAt("recipe params", body, formContentWidth, m.width)
	parts := []string{box, "", warnStyle.Render(m.status)}
	parts = append(parts, renderFooter(formHelp{}, m.width, m.showHelp))
	return column(appContentWidth, parts...)
}
