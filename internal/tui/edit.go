package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/theme"
)

// editModel is the in-TUI editor for an existing VM, replacing the round trip
// through $EDITOR for the fields worth changing. The raw editor stays on "E"
// as the escape hatch for anything this form deliberately doesn't expose.
//
// It is a separate model from formModel rather than a mode of it: the create
// form's whole centre of gravity is the image picker and its download, none
// of which applies to a VM that already exists.
//
// There is no mode field here: mode used to be editable (live/disk/cloud),
// but core.Update holds it immutable (see core/update.go's checkImmutable),
// and this form must not be looser than core. Removing the row is a real,
// deliberate feature removal, not an oversight.
type editModel struct {
	vm     *config.VM
	inputs []textinput.Model
	focus  int
	err    string

	recipeNames []string
	recipeIdx   int
	recipeSel   map[string]bool
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
)

func newEdit(v *config.VM) editModel {
	e := editModel{vm: v, recipeSel: map[string]bool{}}
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

	// Recipes are offered for the VM's own os/backend pair, exactly as the
	// create form does, so a cloud VM is never offered a shell recipe. This
	// list is built once: unlike the old mode switch, nothing on this form
	// can change the (OS, backend) pair, so it never needs resyncing.
	names, _ := recipes.List(v.OS, backendOf(v))
	e.recipeNames = names
	for _, r := range v.Recipes {
		e.recipeSel[r] = true
	}
	return e
}

// backendOf reports the provisioning backend for v, deriving it from Mode
// when the field is empty. VMs created before the backend field existed have
// no value, and defaulting those to "ssh" would offer an Alpine VM the wrong
// recipes.
func backendOf(v *config.VM) string {
	if v.Backend != "" {
		return v.Backend
	}
	if v.Mode == "live" {
		return "apkovl"
	}
	return "ssh"
}

// parseSize is core.ParseSize. edit.go no longer calls this itself (disk
// size validation moved to core.Update), but form.go's create-VM disk field
// still does its own presentational parse before a Spec is ever built, the
// same way buildPatch's RAM/CPUs/SSHPort fields do here, and needs a name
// for it.
var parseSize = core.ParseSize

// name is this VM's identity for core.Update and config.Load: the
// directory, never the vm.toml `name` field, which can diverge from it (see
// core.VM.Name's own comment on why the directory is authoritative). e.vm.Dir
// is always set, either by config.Load or by a VM this form saved earlier.
func (e editModel) name() string { return filepath.Base(e.vm.Dir) }

// order is the tab-traversal order for the VM's mode. Rows viewEdit does not
// draw are omitted rather than included-but-hidden, so focus can never land
// somewhere invisible and edit a field the user can't see.
func (e editModel) order() []int {
	o := []int{eRAM, eCPUs}
	if e.vm.Mode != "live" {
		o = append(o, eDisk)
	}
	o = append(o, eShare, eSSHPort)
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

// buildPatch turns the form's text into a core.Patch naming only the fields
// the user actually touched: a Patch pointer left nil never reaches
// core.Update, so a field the user never typed into can't be silently
// rewritten (notably Share, whose read-expands/write-verbatim asymmetry with
// "~" means setting it unconditionally would rewrite every edited VM's
// share path to an absolute one).
//
// This function is deliberately thin. RAM/CPUs/SSHPort's numeric ranges, the
// disk shrink guard and the ssh port collision check all used to live here;
// core.Update enforces every one of them now, under config.Lock(), against
// fresh data rather than a UI snapshot. Duplicating a rule core already owns
// is exactly how the two drifted before: this form's disk shrink guard used
// to skip itself outright when the VM's stored size wouldn't parse ("+8G",
// the exact value a past release wrote), silently truncating the image.
// What is left here is presentational only: an unparseable integer gets an
// inline error before it ever reaches core, rather than a vaguer one core
// would give for a field it can't even parse into a number.
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
// pane shows inline, keyed by errors.Is rather than the wrapped text, so a
// wording change inside core can't silently stop matching here.
func editErrorText(err error) string {
	// strip drops a sentinel's own text wherever it appears in the wrapped
	// message: validateSSHPort re-wraps validateForwards' own ErrInvalidSpec
	// as "ssh port: %w", so the sentinel text is not always a prefix.
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
		// Unreachable from this form: buildPatch never sets Name, OS,
		// Backend or Mode. Kept so a future field added here without
		// checking core's mutability rules fails with a clear message
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

// saveEdit applies p to name through core.Update: the disk resize (if any)
// and the vm.toml write both happen inside that call, under config.Lock(),
// exactly like every other core mutator. It is a plain function, not a
// tea.Cmd, and is called synchronously from updateEdit's "enter" handler so
// a validation failure can be shown inline in m.edit.err immediately, in the
// same place buildPatch's own presentational errors go, rather than as a
// toast. The IO this does (qemu-img resize, when the disk changed) is the
// same call this form used to make from inside a tea.Cmd; the UI blocks for
// it either way, since nothing here overlapped it with a spinner before.
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
			if m.edit.focus == eRecipes {
				if n := len(m.edit.recipeNames); n > 0 {
					d := 1
					if msg.String() == "left" {
						d = -1
					}
					m.edit.recipeIdx = (m.edit.recipeIdx + d + n) % n
				}
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

// editContentWidth matches the create form, so moving between the two panes
// doesn't shift the layout under the user. Derived rather than restated: as
// a second literal it had already drifted once, leaving this comment
// claiming a match that wasn't there.
const editContentWidth = formContentWidth

func (m model) viewEdit() string {
	e := m.edit
	if e.vm == nil {
		return pane("edit", dimStyle.Render("no vm selected"), m.width)
	}

	b := fields{width: editContentWidth}
	// row draws a field. A changed field carries a dim "was X" so the pane
	// shows what is about to be written versus what is on disk. An edit form
	// that looks identical whether or not you have touched it makes it far
	// too easy to save something you didn't mean to.
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

	parts := []string{box, ""}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	parts = append(parts, renderFooter(editHelp{}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
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
