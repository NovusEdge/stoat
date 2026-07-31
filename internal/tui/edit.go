package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
)

// editModel is the in-TUI editor for an existing VM, replacing the round trip
// through $EDITOR for the fields worth changing. The raw editor stays on "E"
// as the escape hatch for anything this form deliberately doesn't expose.
//
// It is a separate model from formModel rather than a mode of it: the create
// form's whole centre of gravity is the image picker and its download, none
// of which applies to a VM that already exists.
type editModel struct {
	vm     *config.VM
	inputs []textinput.Model
	focus  int
	mode   string
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
	eMode = eFieldCount + iota
	eRecipes
)

// editModes is the cycle offered on the mode row. "cloud" is included but
// guarded at save time — see validate.
var editModes = []string{"live", "disk", "cloud"}

func newEdit(v *config.VM) editModel {
	e := editModel{vm: v, mode: v.Mode, recipeSel: map[string]bool{}}
	vals := []string{
		strconv.Itoa(v.RAM),
		strconv.Itoa(v.CPUs),
		v.Disk,
		v.Share,
		strconv.Itoa(v.SSHPort),
	}
	for i := 0; i < eFieldCount; i++ {
		ti := textinput.New()
		ti.Prompt = ""
		ti.SetValue(vals[i])
		e.inputs = append(e.inputs, ti)
	}
	// A cloud VM carries no size of its own, so the field is empty and the
	// placeholder says what leaving it that way means.
	if v.Mode == "cloud" {
		e.inputs[eDisk].Placeholder = "unchanged"
	}
	e.inputs[eRAM].Focus()

	// Recipes are offered for the VM's own os/backend pair, exactly as the
	// create form does, so a cloud VM is never offered a shell recipe.
	names, _ := recipes.List(v.OS, backendOf(v))
	e.recipeNames = names
	for _, r := range v.Recipes {
		e.recipeSel[r] = true
	}
	return e
}

// brokenPortHolder reports the name of a broken VM directory whose raw
// vm.toml claims port, or "" if none does. Broken VMs never reach m.vms, so
// the caller cannot see them any other way.
func brokenPortHolder(port int, self string) string {
	broken, err := config.ListBroken()
	if err != nil {
		return ""
	}
	for _, b := range broken {
		if b.Name == self {
			continue
		}
		if p, err := config.BrokenSSHPort(b.Name); err == nil && p == port {
			return b.Name
		}
	}
	return ""
}

// backendForMode is the backend a VM would use in the given mode. Only the
// cloud pairing is forced: the cloudinit seed exists solely for cloud mode,
// and a VM leaving it has to fall back to ssh. A disk-mode Alpine VM keeps
// apkovl, which is why this is not a simple mode->backend table.
func backendForMode(v *config.VM, mode string) string {
	if mode == "cloud" {
		return "cloudinit"
	}
	if backendOf(v) == "cloudinit" {
		return "ssh"
	}
	return backendOf(v)
}

// backendOf reports the provisioning backend for v, deriving it from Mode
// when the field is empty — VMs created before the backend field existed have
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

// order is the tab-traversal order for the current mode. Rows viewEdit does
// not draw are omitted rather than included-but-hidden, so focus can never
// land somewhere invisible and edit a field the user can't see.
func (e editModel) order() []int {
	o := []int{eMode, eRAM, eCPUs}
	if e.mode != "live" {
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
	if e.mode != e.vm.Mode {
		return true
	}
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

// syncRecipes re-reads the recipes available for the currently selected
// mode's backend and drops any selection that is no longer on offer.
func (e *editModel) syncRecipes() {
	names, _ := recipes.List(e.vm.OS, backendForMode(e.vm, e.mode))
	e.recipeNames = names
	valid := map[string]bool{}
	for _, n := range names {
		valid[n] = true
	}
	for n := range e.recipeSel {
		if !valid[n] {
			delete(e.recipeSel, n)
		}
	}
	if e.recipeIdx >= len(names) {
		e.recipeIdx = 0
	}
	// The recipes row leaves the focus order when it empties.
	if e.focus == eRecipes && len(names) == 0 {
		e.focus = eSSHPort
		e.refocus()
	}
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

// applied is the VM as edited, plus whether the disk needs resizing. It never
// mutates the live VM: the caller only adopts it once the write succeeds, so
// a failed save can't leave the pane showing state that was never persisted.
type applied struct {
	vm         config.VM
	resizeTo   string // non-empty when the qcow2 must be grown
	sshChanged bool
}

// validate turns the form's text into a VM, refusing the edits that would
// destroy data or collide. The guards are the whole point of this function:
// mode, disk and sshport are all editable here, and all three can break a VM
// that currently works.
func (e editModel) validate(others []*config.VM) (*applied, error) {
	ram, err := strconv.Atoi(strings.TrimSpace(e.inputs[eRAM].Value()))
	if err != nil || ram < 256 {
		return nil, fmt.Errorf("ram must be a number of MB, at least 256")
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(e.inputs[eCPUs].Value()))
	if err != nil || cpus < 1 {
		return nil, fmt.Errorf("cpus must be at least 1")
	}
	port, err := strconv.Atoi(strings.TrimSpace(e.inputs[eSSHPort].Value()))
	if err != nil || port < 1024 || port > 65535 {
		return nil, fmt.Errorf("ssh port must be between 1024 and 65535")
	}
	for _, o := range others {
		if o.Name != e.vm.Name && o.SSHPort == port {
			return nil, fmt.Errorf("port %d is already used by %s", port, o.Name)
		}
	}
	// config.List drops VMs whose vm.toml won't parse, but the port such a VM
	// was using is still committed to its disk image. config.FreePort goes out
	// of its way to scan those; not doing the same here would re-open the
	// collision from the other side.
	if name := brokenPortHolder(port, e.vm.Name); name != "" {
		return nil, fmt.Errorf("port %d is already used by %s (broken vm.toml)", port, name)
	}

	next := *e.vm
	next.RAM = ram
	next.CPUs = cpus
	next.Share = strings.TrimSpace(e.inputs[eShare].Value())
	next.SSHPort = port
	next.Mode = e.mode

	// A cloud VM boots from an overlay of a downloaded base image. Without
	// that base there is nothing to boot, so the mode cannot simply be
	// switched on.
	if e.mode == "cloud" && next.Base == "" {
		return nil, fmt.Errorf("cloud mode needs a base image — create a new VM from the catalog instead")
	}
	// The mirror of the above. A cloud VM has no ISO, and ISOPath() on an
	// empty ISO resolves to the data root DIRECTORY, so qemu is handed
	// "-cdrom ~/.stoat" and refuses to start.
	if e.mode != "cloud" && next.ISO == "" {
		return nil, fmt.Errorf("%s mode needs an iso — this vm only has a cloud base image", e.mode)
	}
	// Backend follows the mode where the two are inseparable: only a cloud
	// VM can use the cloudinit seed, and a VM leaving cloud mode has to fall
	// back to ssh provisioning. Otherwise the VM's own backend is kept.
	next.Backend = backendForMode(e.vm, e.mode)

	out := &applied{vm: next, sshChanged: port != e.vm.SSHPort}

	// Only a disk-mode VM has a size of its own. A cloud VM boots a CoW
	// overlay of its base image and carries no size in vm.toml at all, so
	// demanding one here rejected every cloud VM's first save; there the
	// field is an optional "grow the overlay to". A live VM has no disk.
	if size := strings.TrimSpace(e.inputs[eDisk].Value()); e.mode == "disk" && size == "" {
		return nil, fmt.Errorf("disk size is required in disk mode")
	} else if e.mode != "live" && size != "" {
		if size != e.vm.Disk {
			oldBytes, err1 := parseSize(e.vm.Disk)
			newBytes, err2 := parseSize(size)
			if err2 != nil {
				return nil, fmt.Errorf("disk size: %v", err2)
			}
			// Shrinking a qcow2 truncates it and corrupts the filesystem
			// inside. qemu-img will happily do it; stoat won't.
			if err1 == nil && newBytes < oldBytes {
				return nil, fmt.Errorf("disk can only grow (%s → %s would destroy data)", e.vm.Disk, size)
			}
			out.resizeTo = size
		}
		next.Disk = size
		out.vm = next
	}

	var picked []string
	for _, n := range e.recipeNames {
		if e.recipeSel[n] {
			picked = append(picked, n)
		}
	}
	out.vm.Recipes = picked
	return out, nil
}

// parseSize reads qemu-img's size syntax ("8G", "512M") as bytes. Only the
// suffixes stoat itself writes are supported; anything else is refused rather
// than guessed at, since guessing wrong here means a wrong resize.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	// qemu-img also accepts relative sizes ("+8G"), and strconv.ParseFloat
	// happily reads "+8" as 8 — so "+8G" would pass the grow check as "8G"
	// and then ADD 8G, leaving vm.toml recording "+8G" and disagreeing with
	// the disk forever after. Refused rather than translated.
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("use an absolute size like 16G, not a relative one")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	case 'T':
		mult, s = 1<<40, s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("use a size like 8G or 512M")
	}
	return int64(n * float64(mult)), nil
}

type vmSavedMsg struct {
	vm      *config.VM
	restart bool
	note    string
}

// saveEdit writes the VM and grows its disk if asked. The resize runs BEFORE
// the config write: if qemu-img fails, vm.toml still describes the disk that
// actually exists rather than one that doesn't.
func saveEdit(a *applied, running bool) tea.Cmd {
	return func() tea.Msg {
		v := a.vm
		_, diskErr := os.Stat(v.DiskPath())
		missing := os.IsNotExist(diskErr)

		// A live VM records a disk size (the create form writes one for every
		// non-cloud VM) but has no disk.qcow2, because buildVM only creates
		// one in disk mode. Switching such a VM to disk mode therefore has to
		// create the file, or the save "succeeds" and the next start dies on
		// a missing image with no hint that this form caused it.
		if v.Mode == "disk" && missing {
			if running {
				return errMsg("stop " + v.Name + " first — switching to disk mode has to create its disk")
			}
			size := a.resizeTo
			if size == "" {
				size = v.Disk
			}
			out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), size).CombinedOutput()
			if err != nil {
				return errMsg("qemu-img create: " + strings.TrimSpace(string(out)) + " (nothing was saved)")
			}
			v.Disk = size
			a.resizeTo = "" // created at the requested size already
		}

		if a.resizeTo != "" {
			if running {
				return errMsg("stop " + v.Name + " before resizing its disk (nothing was saved)")
			}
			// A cloud VM's overlay is created lazily on first start, so before
			// then there is nothing to grow. Say so rather than letting
			// qemu-img fail with "Could not open".
			if missing {
				return errMsg("start " + v.Name + " once before growing its disk (nothing was saved)")
			}
			out, err := exec.Command("qemu-img", "resize", v.DiskPath(), a.resizeTo).CombinedOutput()
			if err != nil {
				return errMsg("qemu-img resize: " + strings.TrimSpace(string(out)) + " (nothing was saved)")
			}
		}
		if err := v.Save(); err != nil {
			return errMsg(err.Error())
		}
		note := ""
		switch {
		case running && a.sshChanged:
			note = " — restart to apply (ssh port changes with it)"
		case running:
			note = " — restart to apply"
		case a.resizeTo != "":
			note = " — disk grown to " + a.resizeTo + "; grow the filesystem inside the guest too"
		}
		return vmSavedMsg{vm: &v, restart: running, note: note}
	}
}

func (m model) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.screen = screenDetail
			m.showHelp = false
			// Re-arm the detail screen's ticker. It only re-arms itself while
			// screen == screenDetail, so the visit to this form killed it —
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
			switch m.edit.focus {
			case eMode:
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				i := 0
				for j, md := range editModes {
					if md == m.edit.mode {
						i = j
					}
				}
				m.edit.mode = editModes[(i+d+len(editModes))%len(editModes)]
				// The recipe list is per-backend, and the backend follows the
				// mode. Without this the picker keeps offering (and validate
				// keeps writing) a cloud fragment to a now-live VM, which the
				// ssh path would try to execute as a shell script.
				m.edit.syncRecipes()
				return m, nil
			case eRecipes:
				if n := len(m.edit.recipeNames); n > 0 {
					d := 1
					if msg.String() == "left" {
						d = -1
					}
					m.edit.recipeIdx = (m.edit.recipeIdx + d + n) % n
				}
				return m, nil
			}
		case keySpace:
			if m.edit.focus == eRecipes && len(m.edit.recipeNames) > 0 {
				n := m.edit.recipeNames[m.edit.recipeIdx]
				m.edit.recipeSel[n] = !m.edit.recipeSel[n]
				return m, nil
			}
		case "enter":
			a, err := m.edit.validate(m.vms)
			if err != nil {
				m.edit.err = err.Error()
				return m, nil
			}
			m.edit.err = ""
			return m, saveEdit(a, qemu.Running(m.edit.vm))
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
// doesn't shift the layout under the user.
const editContentWidth = 56

func (m model) viewEdit() string {
	e := m.edit
	if e.vm == nil {
		return pane("edit", dimStyle.Render("no vm selected"), m.width)
	}

	b := fields{width: editContentWidth}
	// row draws a field. A changed field carries a dim "was X" so the pane
	// shows what is about to be written versus what is on disk — an edit form
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

	modeParts := make([]string, len(editModes))
	for i, md := range editModes {
		modeParts[i] = radio(modeLabel(md), md == e.mode)
	}
	modeRow := strings.Join(modeParts, "  ")
	if e.mode != e.vm.Mode {
		modeRow += warnStyle.Render("  " + glyphWas + " was " + e.vm.Mode)
	}
	row(eMode, "mode", modeRow)
	b.hint(modeHint(e.mode))
	b.gap()

	row(eRAM, "ram", e.inputs[eRAM].View()+dimStyle.Render(" MB"))
	row(eCPUs, "cpus", e.inputs[eCPUs].View())
	// The disk row is drawn only where it means something: a size of its own
	// in disk mode, an optional "grow the overlay to" in cloud mode, and
	// nothing at all for a live VM, which has no disk.
	if e.mode != "live" {
		disk := e.inputs[eDisk].View()
		// The hint is dropped once the field is changed, so the "was X"
		// marker sits right next to the value it refers to.
		if e.changed(eDisk) == "" {
			disk += dimStyle.Render("  (grow only)")
		}
		row(eDisk, "disk", disk)
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
		note(warnStyle.Render("running — ram/cpus/ssh apply on restart"))
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
		return dimStyle.Render("— (no matching recipes)")
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
