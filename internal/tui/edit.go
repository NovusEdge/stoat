package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/theme"
)

// editModel is the in-TUI editor for an existing VM. It replaces the round
// trip through $EDITOR for the fields worth changing. "E" still opens the
// raw editor for anything this form does not expose.
//
// editModel is a separate model from formModel, not a mode of it. The create
// form centers on the image picker and its download. Neither applies to a
// VM that already exists.
//
// The form has no mode field. core.Update holds Mode immutable (see
// core/update.go's checkImmutable), so this form must not allow it either.
type editModel struct {
	vm     *config.VM
	inputs []textinput.Model
	focus  int
	err    string

	recipeNames []string
	recipeIdx   int
	recipeSel   map[string]bool

	display string // one of displayChoices; seeded from vm.Display, "" reads as "auto"
}

// edit field indices
const (
	eRAM = iota
	eCPUs
	eDisk
	eShare
	eSSHPort
	eFieldCount
)

// focus positions past the text inputs
const (
	eRecipes = eFieldCount + iota
	eDisplay
)

func newEdit(v *config.VM) editModel {
	e := editModel{vm: v, recipeSel: map[string]bool{}, display: displayPrefLabel(v.Display)}
	vals := []string{
		strconv.Itoa(v.RAM),
		strconv.Itoa(v.CPUs),
		v.Disk,
		v.Share,
		strconv.Itoa(v.SSHPort),
	}
	for i := 0; i < eFieldCount; i++ {
		ti := theme.TextInput()
		ti.SetValue(vals[i])
		e.inputs = append(e.inputs, ti)
	}
	// A cloud VM carries no size of its own, so the field is empty and the
	// placeholder says what leaving it that way means.
	if v.Mode == "cloud" {
		e.inputs[eDisk].Placeholder = "unchanged"
		// See the note in newForm: without a width v2 cuts the placeholder to
		// one rune. viewEdit draws "grow only" as a hint line rather than after
		// the value so that this row appends nothing to the input.
		e.inputs[eDisk].SetWidth(editContentWidth - fieldValueColumn)
	}
	e.inputs[eRAM].Focus()

	// Recipes match the VM's own os/backend pair, the same rule the create
	// form uses. A cloud VM never sees a shell recipe this way. The list is
	// built once here: nothing on this form can change OS or backend, so it
	// never needs a resync.
	names, _ := recipes.List(v.OS, backendOf(v))
	e.recipeNames = names
	for _, r := range v.Recipes {
		e.recipeSel[r] = true
	}
	return e
}

// backendOf reports the provisioning backend for v. It derives the backend
// from Mode when the field is empty. VMs created before the backend field
// existed have no value. Defaulting those to "ssh" would offer an Alpine VM
// the wrong recipes.
func backendOf(v *config.VM) string {
	if v.Backend != "" {
		return v.Backend
	}
	if v.Mode == "live" {
		return "apkovl"
	}
	return "ssh"
}

// parseSize is core.ParseSize under a local name. core.Update now owns disk
// size validation, so edit.go no longer calls this. form.go's create-VM
// disk field still needs it for its own presentational parse, the same
// role buildPatch's RAM/CPUs/SSHPort checks play here.
var parseSize = core.ParseSize

// name is this VM's identity for core.Update and config.Load: the
// directory, never the vm.toml `name` field, which can diverge from it (see
// core.VM.Name's own comment on why the directory is authoritative).
// e.vm.Dir is always set, either by config.Load or by a VM this form saved
// earlier.
func (e editModel) name() string { return filepath.Base(e.vm.Dir) }

// order is the tab-traversal order for the VM's mode. It omits rows viewEdit
// does not draw. Focus can then never land on a hidden field and edit
// something the user cannot see.
func (e editModel) order() []int {
	o := []int{eRAM, eCPUs}
	if e.vm.Mode != "live" {
		o = append(o, eDisk)
	}
	o = append(o, eShare, eSSHPort, eDisplay)
	if len(e.recipeNames) > 0 {
		o = append(o, eRecipes)
	}
	return o
}

// changed reports the original value of field i when the input differs from
// it, for the "was X" marker. Empty means unchanged.
func (e editModel) changed(i int) string {
	was := e.original(i)
	if strings.TrimSpace(e.inputs[i].Value()) == was {
		return ""
	}
	if was == "" {
		return "unset"
	}
	return was
}

func (e editModel) original(i int) string {
	switch i {
	case eRAM:
		return strconv.Itoa(e.vm.RAM)
	case eCPUs:
		return strconv.Itoa(e.vm.CPUs)
	case eDisk:
		return e.vm.Disk
	case eShare:
		return e.vm.Share
	case eSSHPort:
		return strconv.Itoa(e.vm.SSHPort)
	}
	return ""
}

// dirty reports whether anything at all differs from the VM on disk, so the
// footer can say whether "enter" would write nothing.
func (e editModel) dirty() bool {
	for i := 0; i < eFieldCount; i++ {
		if e.changed(i) != "" {
			return true
		}
	}
	was := map[string]bool{}
	for _, r := range e.vm.Recipes {
		was[r] = true
	}
	for _, n := range e.recipeNames {
		if e.recipeSel[n] != was[n] {
			return true
		}
	}
	return false
}

func (e *editModel) refocus() {
	for i := range e.inputs {
		if i == e.focus {
			e.inputs[i].Focus()
		} else {
			e.inputs[i].Blur()
		}
	}
}

func nextIn(o []int, focus int) int {
	for i, f := range o {
		if f == focus {
			return o[(i+1)%len(o)]
		}
	}
	return o[0]
}

func prevIn(o []int, focus int) int {
	for i, f := range o {
		if f == focus {
			return o[(i-1+len(o))%len(o)]
		}
	}
	return o[0]
}

// buildPatch turns the form's text into a core.Patch. It names only the
// fields the user touched. A nil Patch pointer never reaches core.Update, so
// an untouched field can't be silently rewritten. Share needs this rule most:
// its read-expands/write-verbatim asymmetry with "~" would otherwise rewrite
// every edited VM's share path to an absolute one.
//
// core.Update enforces RAM/CPUs/SSHPort ranges, the disk shrink guard and
// the ssh port collision check, under config.Lock() against fresh data.
// buildPatch does not duplicate any of those checks. It only catches a
// value that fails to parse into a number at all, and returns an inline
// error for it before core sees the field.
func (e editModel) buildPatch() (core.Patch, error) {
	ram, err := strconv.Atoi(strings.TrimSpace(e.inputs[eRAM].Value()))
	if err != nil {
		return core.Patch{}, fmt.Errorf("ram must be a number of MB")
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(e.inputs[eCPUs].Value()))
	if err != nil {
		return core.Patch{}, fmt.Errorf("cpus must be a number")
	}
	port, err := strconv.Atoi(strings.TrimSpace(e.inputs[eSSHPort].Value()))
	if err != nil {
		return core.Patch{}, fmt.Errorf("ssh port must be a number")
	}

	var p core.Patch
	if ram != e.vm.RAM {
		p.RAM = &ram
	}
	if cpus != e.vm.CPUs {
		p.CPUs = &cpus
	}
	if share := strings.TrimSpace(e.inputs[eShare].Value()); share != e.vm.Share {
		p.Share = &share
	}
	if port != e.vm.SSHPort {
		p.SSHPort = &port
	}
	// Only a disk-mode or cloud-mode VM has a size of its own, and the row
	// isn't even drawn for a live VM (see order()), so a live VM's input is
	// stale and must not be read.
	if e.vm.Mode != "live" {
		if size := strings.TrimSpace(e.inputs[eDisk].Value()); size != e.vm.Disk {
			p.Disk = &size
		}
	}

	// "auto" writes back as "", matching config.VM.Display's own default so
	// a VM edited back to auto looks the same as one that never set a
	// preference.
	if e.display != displayPrefLabel(e.vm.Display) {
		v := e.display
		if v == "auto" {
			v = ""
		}
		p.Display = &v
	}

	var picked []string
	for _, n := range e.recipeNames {
		if e.recipeSel[n] {
			picked = append(picked, n)
		}
	}
	if !sameRecipes(picked, e.vm.Recipes) {
		p.Recipes = &picked
	}
	return p, nil
}

func sameRecipes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// editErrorText renders one of core.Update's errors as the sentence this
// pane shows inline. It keys on errors.Is, not the wrapped text, so a
// wording change inside core cannot silently break the match here.
func editErrorText(err error) string {
	// strip drops a sentinel's own text wherever it appears in the wrapped
	// message. validateSSHPort re-wraps validateForwards' ErrInvalidSpec as
	// "ssh port: %w", so the sentinel text is not always a prefix.
	strip := func(sentinel error) string {
		return strings.Replace(err.Error(), sentinel.Error()+": ", "", 1)
	}
	switch {
	case errors.Is(err, core.ErrDiskShrink):
		return "disk can only grow (" + strip(core.ErrDiskShrink) + " would destroy data)"
	case errors.Is(err, core.ErrAlreadyRunning):
		return strip(core.ErrAlreadyRunning)
	case errors.Is(err, core.ErrNotFound):
		return "this vm no longer exists"
	case errors.Is(err, core.ErrInvalidSpec):
		return strip(core.ErrInvalidSpec)
	case errors.Is(err, core.ErrImmutableField):
		// buildPatch never sets Name, OS, Backend or Mode, so this case is
		// unreachable today. It stays so a future field added here
		// without checking core's mutability rules gets a clear message
		// instead of a raw, unmapped one.
		return "can't be changed here: " + strip(core.ErrImmutableField)
	default:
		return err.Error()
	}
}

type vmSavedMsg struct {
	vm      *config.VM
	restart bool
	note    string
}

// saveEdit applies p to name through core.Update. The disk resize, when the
// disk changed, and the vm.toml write both happen inside that call, under
// config.Lock(), like every other core mutator. saveEdit is a plain
// function, not a tea.Cmd. updateEdit's "enter" handler calls it
// synchronously, so a validation failure lands in m.edit.err immediately,
// next to buildPatch's own presentational errors, not as a toast. The qemu-img
// resize blocks the UI for the duration of the call.
func saveEdit(name string, p core.Patch, running bool) (msg tea.Msg, errText string) {
	if _, err := core.Update(name, p); err != nil {
		return nil, editErrorText(err)
	}
	v, err := config.Load(name)
	if err != nil {
		return nil, err.Error()
	}
	note := ""
	switch {
	case running && p.SSHPort != nil:
		note = ": restart to apply (ssh port changes with it)"
	case running:
		note = ": restart to apply"
	case p.Disk != nil:
		note = ": disk grown to " + v.Disk + "; grow the filesystem inside the guest too"
	}
	return vmSavedMsg{vm: v, restart: running, note: note}, ""
}

func (m model) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.screen = screenDetail
			m.showHelp = false
			// Re-arm the detail screen's ticker. It only re-arms itself while
			// screen == screenDetail, so the visit to this form killed it,
			// leaving uptime, running state and the provision-log tail frozen
			// until the user went out to the list and back.
			m.detailGen++
			return m, tick(m.detailGen)
		case "?":
			// Same rule as the create form: "?" is a character while a text
			// field has focus, and a help toggle otherwise.
			if m.edit.focus >= eFieldCount {
				m.showHelp = !m.showHelp
				return m, nil
			}
		case "tab", "down":
			m.edit.focus = nextIn(m.edit.order(), m.edit.focus)
			m.edit.refocus()
			return m, nil
		case "shift+tab", "up":
			m.edit.focus = prevIn(m.edit.order(), m.edit.focus)
			m.edit.refocus()
			return m, nil
		case "left", "right":
			d := 1
			if msg.String() == "left" {
				d = -1
			}
			if m.edit.focus == eRecipes {
				if n := len(m.edit.recipeNames); n > 0 {
					m.edit.recipeIdx = (m.edit.recipeIdx + d + n) % n
				}
			}
			if m.edit.focus == eDisplay {
				m.edit.display = cycle(displayChoices, m.edit.display, d)
			}
			return m, nil
		case keySpace:
			if m.edit.focus == eRecipes && len(m.edit.recipeNames) > 0 {
				n := m.edit.recipeNames[m.edit.recipeIdx]
				m.edit.recipeSel[n] = !m.edit.recipeSel[n]
				return m, nil
			}
		case "enter":
			p, err := m.edit.buildPatch()
			if err != nil {
				m.edit.err = err.Error()
				return m, nil
			}
			saved, errText := saveEdit(m.edit.name(), p, qemu.Running(m.edit.vm))
			if errText != "" {
				m.edit.err = errText
				return m, nil
			}
			m.edit.err = ""
			return m, func() tea.Msg { return saved }
		}
	}

	if m.edit.focus < eFieldCount {
		var cmd tea.Cmd
		m.edit.inputs[m.edit.focus], cmd = m.edit.inputs[m.edit.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

// editContentWidth matches the create form's width, so switching between the
// two panes does not shift the layout. It is derived from formContentWidth,
// not a separate literal, so the two cannot drift apart again.
const editContentWidth = formContentWidth

func (m model) viewEdit() string {
	e := m.edit
	if e.vm == nil {
		return pane("edit", dimStyle.Render("no vm selected"), m.width)
	}

	b := fields{width: editContentWidth}
	// row draws a field. A changed field carries a dim "was X" marker, so the
	// pane shows what is about to be written next to what is on disk.
	row := func(i int, label, value string) {
		marker := "  "
		if e.focus == i {
			marker = selStyle.Render(glyphCursor)
			// Text inputs carry their own cursor styling; wrapping them would
			// end the accent at the cursor's reset (see viewForm).
			if i >= eFieldCount {
				value = selStyle.Render(value)
			}
		}
		if i < eFieldCount {
			if was := e.changed(i); was != "" {
				value += warnStyle.Render("  " + glyphWas + " was " + was)
			}
		}
		b.row(marker, label, value)
	}

	row(eRAM, "ram", e.inputs[eRAM].View()+dimStyle.Render(" MB"))
	row(eCPUs, "cpus", e.inputs[eCPUs].View())
	// The disk row is drawn only where it means something: a size of its own
	// in disk mode, an optional "grow the overlay to" in cloud mode, and
	// nothing at all for a live VM, which has no disk.
	if e.vm.Mode != "live" {
		row(eDisk, "disk", e.inputs[eDisk].View())
		// The hint is dropped once the field is changed, so the "was X"
		// marker sits alone next to the value it refers to.
		if e.changed(eDisk) == "" {
			b.note("(grow only)")
		}
	}
	b.gap()

	row(eShare, "share", e.inputs[eShare].View())
	row(eSSHPort, "ssh", e.inputs[eSSHPort].View())

	displayMarker := "  "
	if e.focus == eDisplay {
		displayMarker = selStyle.Render(glyphCursor)
	}
	displayRow := radio("auto", e.display == "auto") + "  " +
		radio("window", e.display == "window") + "  " +
		radio("vnc", e.display == "vnc")
	if e.focus == eDisplay {
		displayRow = selStyle.Render(displayRow)
	}
	if was := displayPrefLabel(e.vm.Display); e.display != was {
		displayRow += warnStyle.Render("  " + glyphWas + " was " + was)
	}
	b.row(displayMarker, "display", displayRow)
	b.gap()

	// Recipes are only offered when any exist for this VM's os/backend;
	// an empty row is one more thing to tab through for nothing.
	if len(e.recipeNames) > 0 {
		marker := "  "
		if e.focus == eRecipes {
			marker = selStyle.Render(glyphCursor)
		}
		b.row(marker, "recipes", editRecipesLabel(e))
	}

	// Notes about the form as a whole, not about any one field, so they sit
	// under the table at its left edge rather than in the value column.
	body := b.String()
	note := func(s string) { body += "\n" + s }
	if !e.dirty() {
		note(dimStyle.Render("no changes"))
	}
	if qemu.Running(e.vm) {
		note(warnStyle.Render("running: ram/cpus/ssh apply on restart"))
	}
	if e.err != "" {
		note(errStyle.Render(e.err))
	}

	box := paneAt("edit "+e.vm.Name, body, editContentWidth, m.width)

	// The status slot is always present, blank when there is nothing to
	// show, so an appearing message replaces blank space instead of
	// pushing the footer down.
	parts := []string{box, "", warnStyle.Render(m.status)}
	parts = append(parts, renderFooter(editHelp{}, m.width, m.showHelp))
	return column(appContentWidth, parts...)
}

func editRecipesLabel(e editModel) string {
	if len(e.recipeNames) == 0 {
		return dimStyle.Render("(no matching recipes)")
	}
	items := make([]string, len(e.recipeNames))
	for i, name := range e.recipeNames {
		box := "[ ]"
		if e.recipeSel[name] {
			box = "[x]"
		}
		item := box + " " + recipeLabel(name)
		if e.focus == eRecipes && i == e.recipeIdx {
			item = selStyle.Render(item)
		}
		items[i] = item
	}
	return wrapItems(items, editContentWidth-fieldValueColumn, strings.Repeat(" ", fieldValueColumn))
}
