package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/novusedge/stoat/internal/core"
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
}

func (m model) viewParamForm() string {
	body := m.form.paramForm.form.View()
	box := paneAt("recipe params", body, formContentWidth, m.width)
	parts := []string{box, "", warnStyle.Render(m.status)}
	parts = append(parts, renderFooter(formHelp{}, m.width, m.showHelp))
	return column(appContentWidth, parts...)
}
