package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

const downloadEntry = "⤓ download latest Alpine…"

type formModel struct {
	inputs   []textinput.Model // name, ram, cpus, disk, share
	focus    int
	isos     []string
	isoIdx   int
	mode     string // "live" | "disk"
	err      string
	fetching bool
}

// field indices into inputs
const (
	fName = iota
	fRAM
	fCPUs
	fDisk
	fShare
	fieldCount
)

// focus positions beyond the text inputs
const (
	fISO  = fieldCount
	fMode = fieldCount + 1
)

// focusOrder is the tab-traversal order of focus positions, which must match
// the visual order fields are rendered in by viewForm — not the order the
// field constants happen to be declared in.
type focusOrder []int

// order returns the tab-traversal order for the form's current mode: name,
// iso, mode, ram, cpus, [disk], share. fDisk is included only in disk mode,
// since viewForm doesn't render a disk field (or its "❯" marker) in live
// mode — landing focus there would silently edit an invisible field.
func (f formModel) order() focusOrder {
	o := focusOrder{fName, fISO, fMode, fRAM, fCPUs}
	if f.mode == "disk" {
		o = append(o, fDisk)
	}
	return append(o, fShare)
}

func (o focusOrder) indexOf(focus int) int {
	for i, f := range o {
		if f == focus {
			return i
		}
	}
	return 0
}

func (o focusOrder) next(focus int) int {
	return o[(o.indexOf(focus)+1)%len(o)]
}

func (o focusOrder) prev(focus int) int {
	return o[(o.indexOf(focus)-1+len(o))%len(o)]
}

func newForm() formModel {
	f := formModel{mode: "live"}
	labels := []string{"work", "4096", "4", "8G", "~/vms"}
	for i := 0; i < fieldCount; i++ {
		ti := textinput.New()
		ti.SetValue(labels[i])
		ti.Prompt = ""
		f.inputs = append(f.inputs, ti)
	}
	f.inputs[fName].SetValue("")
	f.inputs[fName].Placeholder = "name"
	f.inputs[fName].Focus()
	f.isos, _ = iso.List()
	return f
}

type isoFetchedMsg string
type isoFetchErrMsg string

func fetchISO() tea.Cmd {
	return func() tea.Msg {
		r, err := iso.Latest()
		if err != nil {
			return isoFetchErrMsg("index: " + err.Error())
		}
		path, err := iso.Download(r, nil)
		if err != nil {
			return isoFetchErrMsg("download: " + err.Error())
		}
		return isoFetchedMsg(path)
	}
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case isoFetchedMsg:
		m.form.fetching = false
		m.form.isos, _ = iso.List()
		name := string(msg)
		for i, s := range m.form.isos {
			if "isos/"+s == name {
				m.form.isoIdx = i
			}
		}
		m.status = "downloaded " + name
		return m, nil

	case isoFetchErrMsg:
		m.form.fetching = false
		m.status = string(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.screen = screenList
			m.status = ""
			return m, nil
		case "tab", "down":
			m.form.focus = m.form.order().next(m.form.focus)
			m.form.refocus()
			return m, nil
		case "shift+tab", "up":
			m.form.focus = m.form.order().prev(m.form.focus)
			m.form.refocus()
			return m, nil
		case "left", "right":
			switch m.form.focus {
			case fISO:
				n := len(m.form.isos) + 1 // +1 for the download entry
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				m.form.isoIdx = (m.form.isoIdx + d + n) % n
				return m, nil
			case fMode:
				if m.form.mode == "live" {
					m.form.mode = "disk"
				} else {
					m.form.mode = "live"
				}
				return m, nil
			}
			// any other field: fall through to the text-input update below
			// so the arrow key moves the cursor instead of being swallowed.
		case "enter":
			if m.form.focus == fISO && m.form.isoIdx == len(m.form.isos) {
				if m.form.fetching {
					return m, nil // a fetch is already in flight; don't start a second one
				}
				m.form.fetching = true
				m.status = "downloading…"
				return m, fetchISO()
			}
			vm, err := m.form.build()
			if err != nil {
				m.form.err = err.Error()
				return m, nil
			}
			return m, tea.Sequence(createVM(vm), loadVMs, backToList())
		}
	}

	if m.form.focus < fieldCount {
		var cmd tea.Cmd
		m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func backToList() tea.Cmd {
	return func() tea.Msg { return screenMsg(screenList) }
}

// screenMsg switches the active screen.
type screenMsg screen

func (f *formModel) refocus() {
	for i := range f.inputs {
		if i == f.focus {
			f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
}

// build validates the form and returns the VM it describes.
func (f formModel) build() (*config.VM, error) {
	name := strings.TrimSpace(f.inputs[fName].Value())
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, "/ ") {
		return nil, fmt.Errorf("name cannot contain spaces or slashes")
	}
	if _, err := os.Stat(config.Root() + "/" + name); err == nil {
		return nil, fmt.Errorf("%s already exists", name)
	}
	if len(f.isos) == 0 || f.isoIdx >= len(f.isos) {
		return nil, fmt.Errorf("pick an iso first")
	}
	ram, err := strconv.Atoi(strings.TrimSpace(f.inputs[fRAM].Value()))
	if err != nil || ram < 256 {
		return nil, fmt.Errorf("ram must be a number of MB, at least 256")
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(f.inputs[fCPUs].Value()))
	if err != nil || cpus < 1 {
		return nil, fmt.Errorf("cpus must be at least 1")
	}
	port, err := config.FreePort()
	if err != nil {
		return nil, err
	}
	return &config.VM{
		Name:    name,
		Mode:    f.mode,
		ISO:     "isos/" + f.isos[f.isoIdx],
		RAM:     ram,
		CPUs:    cpus,
		Disk:    strings.TrimSpace(f.inputs[fDisk].Value()),
		Share:   strings.TrimSpace(f.inputs[fShare].Value()),
		SSHPort: port,
	}, nil
}

// buildVM writes vm.toml and, for disk mode, allocates the qcow2. If
// qemu-img fails, the VM directory (and the vm.toml just written) is removed
// so a failed creation leaves no trace in the data root — otherwise the list
// would show a VM with no disk.qcow2 that can never boot.
func buildVM(v *config.VM) error {
	if err := v.Save(); err != nil {
		return err
	}
	if v.Mode == "disk" {
		out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), v.Disk).CombinedOutput()
		if err != nil {
			os.RemoveAll(v.Dir)
			return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// createVM is the tea.Cmd wrapper around buildVM.
func createVM(v *config.VM) tea.Cmd {
	return func() tea.Msg {
		if err := buildVM(v); err != nil {
			return statusMsg(err.Error())
		}
		return statusMsg("created " + v.Name)
	}
}

func (m model) viewForm() string {
	f := m.form
	var b strings.Builder
	b.WriteString(accentStyle.Render("  new vm") + "\n\n")

	row := func(i int, label, value string) {
		marker := "  "
		if f.focus == i {
			marker = selStyle.Render("❯ ")
			value = selStyle.Render(value)
		}
		b.WriteString(fmt.Sprintf("%s%-8s %s\n", marker, label, value))
	}

	row(fName, "name", f.inputs[fName].View())
	isoLabel := downloadEntry
	if f.isoIdx < len(f.isos) {
		isoLabel = f.isos[f.isoIdx]
	}
	row(fISO, "iso", isoLabel)
	modeLabel := "(•) live   ( ) disk"
	if f.mode == "disk" {
		modeLabel = "( ) live   (•) disk"
	}
	row(fMode, "mode", modeLabel)
	row(fRAM, "ram", f.inputs[fRAM].View()+dimStyle.Render(" MB"))
	row(fCPUs, "cpus", f.inputs[fCPUs].View())
	if f.mode == "disk" {
		row(fDisk, "disk", f.inputs[fDisk].View())
	} else {
		b.WriteString(dimStyle.Render("  disk     — (live mode)") + "\n")
	}
	row(fShare, "share", f.inputs[fShare].View())

	if f.fetching {
		b.WriteString("\n  " + dimStyle.Render("downloading latest alpine…") + "\n")
	}
	if f.err != "" {
		b.WriteString("\n" + errStyle.Render("  "+f.err) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("  tab move   ←/→ change   ↵ create   esc cancel") + "\n")
	return b.String()
}
