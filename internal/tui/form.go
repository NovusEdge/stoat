package tui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/recipes"
)

// imageOption is one entry in the new-VM form's image picker: either a
// catalog image (from iso.Catalog(), possibly not yet downloaded) or a
// bring-your-own file sitting in isos/ that doesn't match any catalog entry.
type imageOption struct {
	entry   *iso.Entry // non-nil for a catalog image; nil for BYO
	file    string     // bare filename under isos/, once available locally; "" if a catalog entry hasn't been downloaded yet
	backend string     // entry.Backend for catalog; iso.Infer's guess for BYO (overridable via fBackend)
	osName  string     // entry.OS for catalog; iso.Infer's guess for BYO (unrecognised files: "")
	sshUser string     // entry.SSHUser for catalog; "" for BYO (sshx defaults empty to root)
}

func (o imageOption) isBYO() bool { return o.entry == nil }

// label renders the image picker row for one option.
func (o imageOption) label() string {
	if o.entry != nil {
		status := glyphDownload + " download"
		if o.file != "" {
			status = "downloaded"
		}
		return fmt.Sprintf("%-8s %-10s %s", o.entry.OS, o.entry.Backend, status)
	}
	osLabel := o.osName
	if osLabel == "" {
		osLabel = "?"
	}
	return fmt.Sprintf("%-8s %-10s %s (byo)", osLabel, o.backend, o.file)
}

// localImageFiles lists every plain file under isos/, any extension, so BYO
// qcow2/img cloud images are picked up alongside ISOs.
func localImageFiles() []string {
	entries, err := os.ReadDir(filepath.Join(config.Root(), "isos"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		// .part files are half-finished downloads. iso.Infer happily matches
		// one ("…cloudimg-amd64.img.part" contains "cloudimg"), so without
		// this they show up as selectable BYO images and a VM can be built on
		// a truncated file. Aborting a download is routine — it is minutes
		// long with no cancel key — so these do accumulate.
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".part") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// matchLocalImage reports which local file (if any) satisfies catalog entry
// e: either the exact basename of e.URL (direct-URL entries), or, for
// entries resolved through an index rather than a fixed filename (Alpine),
// whatever local file iso.Infer agrees belongs to e's OS/backend pair.
func matchLocalImage(e iso.Entry, files []string) string {
	if e.OS != "alpine" && e.URL != "" {
		if u, err := url.Parse(e.URL); err == nil {
			base := path.Base(u.Path)
			for _, f := range files {
				if f == base {
					return f
				}
			}
		}
	}
	for _, f := range files {
		backend, osName := iso.Infer(f)
		if backend == e.Backend && osName == e.OS {
			return f
		}
	}
	return ""
}

// buildImages assembles the form's image picker: every catalog entry (in
// Catalog order), each flagged with whether it's already downloaded, then
// every local file that isn't claimed by a catalog entry, as BYO options.
func buildImages() []imageOption {
	files := localImageFiles()
	matched := map[string]bool{}
	var out []imageOption
	for _, e := range iso.Catalog() {
		e := e
		opt := imageOption{entry: &e, backend: e.Backend, osName: e.OS, sshUser: e.SSHUser}
		if f := matchLocalImage(e, files); f != "" {
			opt.file = f
			matched[f] = true
		}
		out = append(out, opt)
	}
	for _, f := range files {
		if matched[f] {
			continue
		}
		backend, osName := iso.Infer(f)
		out = append(out, imageOption{file: f, backend: backend, osName: osName})
	}
	return out
}

// byoBackends is the fixed cycle offered on the fBackend override row.
var byoBackends = []string{"ssh", "apkovl", "cloudinit"}

// formContentWidth holds the new-vm pane at a constant width. Sized to the
// widest thing it ever shows — the download stats line, indented to the
// value column — so the box never resizes as optional rows appear.
const formContentWidth = 56

type formModel struct {
	inputs      []textinput.Model // name, ram, cpus, disk, share
	focus       int
	images      []imageOption
	imgIdx      int
	byoBackend  string // override for the selected BYO image's backend; "" means "use iso.Infer's guess"
	mode        string // "live" | "disk" — meaningful only while the selected image's backend is apkovl
	err         string
	fetching    bool
	fetchingOS  string
	recipeNames []string        // installed recipes matching the selected image's OS/backend
	recipeIdx   int             // sub-cursor within the recipes row, moved by left/right
	recipeSel   map[string]bool // names currently checked
	// randomPassword swaps the fixed, documented console password for a
	// generated one. Cloud images only — see build().
	randomPassword bool
	dl             dlStats // last snapshot of the in-flight download
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
	fISO = fieldCount + iota
	fMode
	fBackend
	fRecipes
	fPassword
)

// focusOrder is the tab-traversal order of focus positions, which must match
// the visual order fields are rendered in by viewForm — not the order the
// field constants happen to be declared in.
type focusOrder []int

// order returns the tab-traversal order for the form's current state: name,
// image, [backend override, BYO only], [mode, apkovl only], ram, cpus,
// [disk, effective disk mode only], share, recipes. Conditional fields are
// omitted rather than included-but-hidden — viewForm doesn't render them in
// those states, so landing focus there would silently edit an invisible
// field (the same reasoning fDisk already followed pre-Task-8). recipes is
// always included, even with zero recipes matching: viewForm always renders
// that row (with a "none" placeholder), so it's always a valid landing spot.
func (f formModel) order() focusOrder {
	o := focusOrder{fName, fISO}
	if opt := f.selected(); opt != nil && opt.isBYO() {
		o = append(o, fBackend)
	}
	if f.resolvedBackend() == "apkovl" {
		o = append(o, fMode)
	}
	o = append(o, fRAM, fCPUs)
	// Cloud VMs need a size as much as disk ones do: the overlay inherits the
	// base image's virtual size, which is sized to boot and nothing more.
	if m := f.effectiveMode(); m == "disk" || m == "cloud" {
		o = append(o, fDisk)
	}
	o = append(o, fShare, fRecipes)
	// The console password row is only meaningful for a cloud image; the
	// other backends never set one.
	if f.resolvedBackend() == "cloudinit" {
		o = append(o, fPassword)
	}
	return o
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

// selected returns the image option under the picker's cursor, or nil if
// the picker has nothing (never happens in practice: iso.Catalog() always
// contributes entries).
func (f formModel) selected() *imageOption {
	if len(f.images) == 0 || f.imgIdx >= len(f.images) {
		return nil
	}
	return &f.images[f.imgIdx]
}

// resolvedBackend is the backend build() will use: entry.Backend for a
// catalog image, iso.Infer's guess for BYO unless overridden via fBackend.
func (f formModel) resolvedBackend() string {
	opt := f.selected()
	if opt == nil {
		return ""
	}
	if opt.isBYO() && f.byoBackend != "" {
		return f.byoBackend
	}
	return opt.backend
}

func (f formModel) resolvedOS() string {
	if opt := f.selected(); opt != nil {
		return opt.osName
	}
	return ""
}

func (f formModel) resolvedSSHUser() string {
	if opt := f.selected(); opt != nil {
		return opt.sshUser
	}
	return ""
}

// effectiveMode is the Mode build() will write. cloudinit is always "cloud"
// (a cloud image boots straight off its overlay, no install step); ssh is
// always "disk" (an unrecognised BYO file is assumed to need a real install,
// then manual/ssh provisioning — the apkovl live path only exists for
// Alpine). apkovl keeps the user-controlled live/disk toggle exactly as
// before Task 8.
func (f formModel) effectiveMode() string {
	switch f.resolvedBackend() {
	case "cloudinit":
		return "cloud"
	case "ssh":
		return "disk"
	default:
		return f.mode
	}
}

// refreshRecipes recomputes the recipe list for the currently selected
// image's OS/backend and clears any selection made against the old list —
// a recipe name from one OS is meaningless once the picker moves to another.
func (f *formModel) refreshRecipes() {
	f.recipeNames, _ = recipes.List(f.resolvedOS(), f.resolvedBackend())
	f.recipeSel = map[string]bool{}
	f.recipeIdx = 0
}

func newForm() formModel {
	f := formModel{mode: "live", recipeSel: map[string]bool{}}
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

	f.images = buildImages()
	// Default to the Alpine catalog entry, so a fresh form behaves exactly
	// like the pre-Task-8 Alpine-only picker: live/disk toggle available,
	// apkovl provisioning, recipes filtered to alpine.
	for i, opt := range f.images {
		if opt.entry != nil && opt.entry.OS == "alpine" {
			f.imgIdx = i
			break
		}
	}
	f.refreshRecipes()
	return f
}

type imageFetchedMsg struct {
	entryID  string
	path     string
	verified bool
}
type imageFetchErrMsg string

// fetchImage downloads e. Re-running it on an image that is already local is
// safe and is how "re-download" works: iso.Download only short-circuits when
// the local file's digest MATCHES the published one, so a truncated or
// superseded file mismatches and is refetched in full. Deleting it first
// would only open a window where a good image is gone and the refetch fails.
func fetchImage(e iso.Entry) tea.Cmd {
	return func() tea.Msg {
		r, err := iso.Resolve(e)
		if err != nil {
			return imageFetchErrMsg(e.OS + ": " + err.Error())
		}
		p, err := iso.Download(r, dlRecord)
		if err != nil {
			return imageFetchErrMsg(e.OS + ": " + err.Error())
		}
		return imageFetchedMsg{entryID: e.ID, path: p, verified: r.Verified}
	}
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dlTickMsg:
		// The chain is anchored to m.form.fetching rather than to a
		// generation counter: only one download can be in flight at a time,
		// so when the fetch ends the chain simply stops re-arming.
		if !m.form.fetching {
			return m, nil
		}
		m.form.dl = dlSnapshot(time.Now())
		return m, dlTick()

	case imageFetchedMsg:
		m.form.fetching = false
		m.form.images = buildImages()
		for i, opt := range m.form.images {
			if opt.entry != nil && opt.entry.ID == msg.entryID {
				m.form.imgIdx = i
			}
		}
		m.form.refreshRecipes()
		note := ""
		if !msg.verified {
			// An unverified image download must be visible, not silent —
			// this is the only consumer of Release.Verified.
			note = " — UNVERIFIED (no checksum)"
		}
		m.status = "downloaded " + msg.path + note
		return m, nil

	case imageFetchErrMsg:
		m.form.fetching = false
		m.status = string(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.screen = screenList
			m.status = ""
			m.showHelp = false
			return m, nil
		case "?":
			// Only a help toggle when no text field has focus — otherwise "?"
			// is just a character the user is trying to type into name/share.
			if m.form.focus < fieldCount {
				break
			}
			m.showHelp = !m.showHelp
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
				n := len(m.form.images)
				if n == 0 {
					return m, nil
				}
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				m.form.imgIdx = (m.form.imgIdx + d + n) % n
				m.form.byoBackend = "" // a new image resets any prior override
				m.form.refreshRecipes()
				return m, nil
			case fBackend:
				cur := m.form.resolvedBackend()
				idx := 0
				for i, b := range byoBackends {
					if b == cur {
						idx = i
					}
				}
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				m.form.byoBackend = byoBackends[(idx+d+len(byoBackends))%len(byoBackends)]
				m.form.refreshRecipes()
				return m, nil
			case fMode:
				if m.form.mode == "live" {
					m.form.mode = "disk"
				} else {
					m.form.mode = "live"
				}
				return m, nil
			case fPassword:
				m.form.randomPassword = !m.form.randomPassword
				return m, nil
			case fRecipes:
				n := len(m.form.recipeNames)
				if n == 0 {
					return m, nil
				}
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				m.form.recipeIdx = (m.form.recipeIdx + d + n) % n
				return m, nil
			}
			// any other field: fall through to the text-input update below
			// so the arrow key moves the cursor instead of being swallowed.
		case " ":
			if m.form.focus == fRecipes && len(m.form.recipeNames) > 0 {
				name := m.form.recipeNames[m.form.recipeIdx]
				if m.form.recipeSel == nil {
					m.form.recipeSel = map[string]bool{}
				}
				m.form.recipeSel[name] = !m.form.recipeSel[name]
				return m, nil
			}
			// space on the image row downloads the selected catalog entry.
			// Pressing it on an image that is already local re-verifies it and
			// refetches only if the bytes no longer match the published digest,
			// so it doubles as "repair this image" at no risk to a good one.
			if m.form.focus == fPassword {
				m.form.randomPassword = !m.form.randomPassword
				return m, nil
			}
			if m.form.focus == fISO {
				opt := m.form.selected()
				if opt == nil || opt.entry == nil {
					m.status = "byo images are already local — nothing to download"
					return m, nil
				}
				if m.form.fetching {
					return m, nil // a fetch is already in flight; don't start a second one
				}
				m.form.fetching = true
				m.form.fetchingOS = opt.entry.OS
				m.form.dl = dlStats{}
				m.status = ""
				dlStart()
				return m, tea.Batch(fetchImage(*opt.entry), dlTick())
			}
		case "enter":
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
	if _, err := os.Stat(filepath.Join(config.Root(), name)); err == nil {
		return nil, fmt.Errorf("%s already exists", name)
	}
	opt := f.selected()
	if opt == nil {
		return nil, fmt.Errorf("pick an image first")
	}
	if opt.file == "" {
		return nil, fmt.Errorf("press space to download %s first", opt.entry.OS)
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
	selected := []string{}
	for _, r := range f.recipeNames {
		if f.recipeSel[r] {
			selected = append(selected, r)
		}
	}

	vm := &config.VM{
		Name:    name,
		Mode:    f.effectiveMode(),
		OS:      f.resolvedOS(),
		Backend: f.resolvedBackend(),
		SSHUser: f.resolvedSSHUser(),
		RAM:     ram,
		CPUs:    cpus,
		Share:   strings.TrimSpace(f.inputs[fShare].Value()),
		SSHPort: port,
		Recipes: selected,
	}

	if vm.Backend == "cloudinit" {
		abs, err := filepath.Abs(filepath.Join(config.Root(), "isos", opt.file))
		if err != nil {
			return nil, err
		}
		vm.Base = abs
		vm.Disk = strings.TrimSpace(f.inputs[fDisk].Value())
		// Only a cloud image needs this. cloud-init locks every account by
		// default, so without a console password the qemu window shows a
		// login prompt with no valid answer. A live Alpine VM already logs
		// root in at the console with no password, and a disk VM's password
		// is whatever the user sets during the guest's own installer.
		pw := config.DefaultConsolePassword
		if f.randomPassword {
			var err error
			if pw, err = config.RandomConsolePassword(); err != nil {
				return nil, err
			}
		}
		vm.ConsolePassword = pw
	} else {
		vm.ISO = "isos/" + opt.file
		vm.Disk = strings.TrimSpace(f.inputs[fDisk].Value())
	}

	return vm, nil
}

// buildVM writes vm.toml and, for disk mode, allocates the qcow2. If
// qemu-img fails, the VM directory (and the vm.toml just written) is removed
// so a failed creation leaves no trace in the data root — otherwise the list
// would show a VM with no disk.qcow2 that can never boot.
//
// Cloud mode's overlay (backed by Base) and cloud-init seed are deliberately
// NOT created here: qemu.Start's ensureCloudOverlay creates them once, on
// first start, since — unlike a live VM's apkovl, rebuilt every start — a
// cloud overlay holds real guest state that must never be discarded, and
// creating it here would also mean creating it again if the user never
// actually starts the VM.
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

	// Rows are single-spaced with a blank line between GROUPS of related
	// fields, not between every row. The form is the tallest screen — nine
	// rows plus a title and a download block — so spacing every row both
	// overflowed a 24-line terminal and read as too airy; grouping gives the
	// separation without the sprawl, and says which fields belong together.
	group := func() { b.WriteString("\n") }

	row := func(i int, label, value string) {
		marker := "  "
		if f.focus == i {
			marker = selStyle.Render(glyphCursor)
			// Text inputs are NOT wrapped: a textinput's view carries its own
			// cursor styling, and a styled substring's \x1b[0m resets the
			// enclosing style too — so wrapping produced a row that was accent
			// up to the cursor and default after it. The ❯ and the input's own
			// cursor already mark focus. Picker rows are plain strings and wrap
			// safely.
			if i >= fieldCount {
				value = selStyle.Render(value)
			}
		}
		fmt.Fprintf(&b, "%s%-8s %s\n", marker, label, value)
	}

	row(fName, "name", f.inputs[fName].View())
	group()

	opt := f.selected()
	imgLabel := "— (none)"
	if opt != nil {
		imgLabel = opt.label()
	}
	row(fISO, "image", imgLabel)

	if opt != nil && opt.isBYO() {
		row(fBackend, "backend", f.resolvedBackend())
	}

	if f.resolvedBackend() == "apkovl" {
		modeRow := radio(modeLabel("live"), f.mode == "live") + "   " + radio(modeLabel("disk"), f.mode == "disk")
		row(fMode, "mode", modeRow)
		b.WriteString(dimStyle.Render("           "+modeHint(f.effectiveMode())) + "\n")
	} else {
		b.WriteString(dimStyle.Render("  mode     "+modeLabel(f.effectiveMode())) + "\n")
		b.WriteString(dimStyle.Render("           "+modeHint(f.effectiveMode())) + "\n")
	}

	group()

	row(fRAM, "ram", f.inputs[fRAM].View()+dimStyle.Render(" MB"))
	row(fCPUs, "cpus", f.inputs[fCPUs].View())
	switch f.effectiveMode() {
	case "disk":
		row(fDisk, "disk", f.inputs[fDisk].View())
	case "cloud":
		row(fDisk, "disk", f.inputs[fDisk].View())
		b.WriteString(dimStyle.Render("           cloud images ship ~2.4G of usable root — raise this to install anything") + "\n")
	default:
		b.WriteString(dimStyle.Render("  disk     — (live mode)") + "\n")
	}
	group()

	row(fShare, "share", f.inputs[fShare].View())

	recipesMarker := "  "
	if f.focus == fRecipes {
		recipesMarker = selStyle.Render(glyphCursor)
	}
	fmt.Fprintf(&b, "%s%-8s %s\n", recipesMarker, "recipes", f.recipesLabel())

	if f.resolvedBackend() == "cloudinit" {
		marker := "  "
		if f.focus == fPassword {
			marker = selStyle.Render(glyphCursor)
		}
		val := radio("stoat", !f.randomPassword) + "  " + radio("random", f.randomPassword)
		fmt.Fprintf(&b, "%s%-8s %s\n", marker, "console", val)
		b.WriteString(dimStyle.Render("           console login for the stoat user") + "\n")
	}

	if f.fetching {
		b.WriteString("\n" + dlView(f.fetchingOS, f.dl))
	}
	if f.err != "" {
		b.WriteString("\n" + errStyle.Render(f.err))
	}

	box := paneAt("new vm", strings.TrimRight(b.String(), "\n"), formContentWidth, m.width)

	// Center rather than Left: the footer is far wider than the box, so a
	// left join pins the box to the footer's left edge and the pane reads as
	// off-center once the whole rectangle is placed.
	parts := []string{box, ""}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	parts = append(parts, renderFooter(formHelp{}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}

// recipesLabel renders the recipes row's checkbox list, highlighting the
// item under the row's sub-cursor when the row itself has focus. An empty
// recipes list (nothing matches the selected image's OS/backend) renders a
// placeholder instead of a blank row, and the row stays a harmless,
// non-crashing landing spot in the focus cycle either way.
func (f formModel) recipesLabel() string {
	if len(f.recipeNames) == 0 {
		return dimStyle.Render("— (no matching recipes)")
	}
	items := make([]string, len(f.recipeNames))
	for i, name := range f.recipeNames {
		box := "[ ]"
		if f.recipeSel[name] {
			box = "[x]"
		}
		item := box + " " + recipeLabel(name)
		if f.focus == fRecipes && i == f.recipeIdx {
			item = selStyle.Render(item)
		}
		items[i] = item
	}
	// 11 = the value column (2-cell marker + 8-cell label + space).
	return wrapItems(items, formContentWidth-11, strings.Repeat(" ", 11))
}
