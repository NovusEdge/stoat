package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/theme"
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

	// A browsed BYO file (byoOptionFromPath) puts an ABSOLUTE path here
	// instead, since the whole point is that it lives outside isos/. Anything
	// resolving this to a real path must go through imagePath, not join it
	// onto isos/ directly.
	//
	// bytes is the image's size and exact is whether it was measured rather
	// than declared: a file already on disk is stat'd, one still to be
	// downloaded carries the catalog's approximation. Resolved once in
	// buildImages, which runs on form-open and after a download, never per
	// render, so the stat costs nothing in the draw path.
	bytes int64
	exact bool
}

// sizeLabel renders the image's size for a picker row: exact for a local
// file, "~" for the catalog's declared approximation, empty when neither is
// known (a BYO file that vanished between ReadDir and Stat).
func (o imageOption) sizeLabel() string {
	if o.bytes <= 0 {
		return ""
	}
	if o.exact {
		return humanBytes(o.bytes)
	}
	return "~" + humanBytes(o.bytes)
}

func (o imageOption) isBYO() bool { return o.entry == nil }

// imagePath resolves file to an absolute path. A browsed BYO image is already
// absolute and lives outside isos/; everything else is a bare name under it.
// filepath.Join does NOT special-case an absolute second element, so joining
// isos/ onto "/home/u/x.iso" yields "…/isos/home/u/x.iso". The two cases
// have to be told apart here rather than at each call site.
func (o imageOption) imagePath() (string, error) {
	if filepath.IsAbs(o.file) {
		return o.file, nil
	}
	return filepath.Abs(filepath.Join(config.Root(), "isos", o.file))
}

// byoFileWidth is the column a BYO filename is truncated into. Fixed so that
// whatever follows it lands in the same place on every row: the "(byo)" tag
// used to trail a variable-length filename, which put it at a different
// column on every line and read as ragged. Real names run long
// (ubuntu-24.04-server-cloudimg-amd64.img is 38 cells) and an untruncated one
// pushed the row past the form pane and wrapped it.
//
// 24, down from 30, to make room for the size column: the BYO row is the
// widest the form draws (os, backend, filename, size, tag) and at 30 the whole
// row came to 65 cells against a 60-cell value column. The filename is the
// only one of the five with slack. Every other column is sized to its
// content, so it is the one that gives.
const byoFileWidth = 24

// osUnknown is shown when iso.Infer could not name a BYO file's OS. It used
// to render as "?", which tells the user neither what happened nor that
// anything is still selectable. This is a state, not an error.
const osUnknown = "unknown"

// imageMetaWidth is the second column of an image row: the catalog entry's
// variant, or a BYO file's backend. It must fit the widest of BOTH, which is
// "13 (trixie)" at 11. At 10 that one row overflowed by a single cell and
// pushed debian's size column one right of every other row's.
// TestImageMetaColumnFitsEveryValue keeps it honest.
const imageMetaWidth = 11

// label renders the image picker row for one option.
//
// Padding happens on the PLAIN strings and styling is applied to whole
// segments afterwards, never inside the format: a styled substring carries
// ANSI bytes that %-8s counts as width, which is how the `ls` output in this
// repo once came out visibly skewed.
func (o imageOption) label() string {
	// Size shares the picker's column width so the form row and the modal
	// agree, and is right-aligned for the same reason: sizes exist to be
	// compared, and comparing is easier when the digits line up.
	size := dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, o.sizeLabel()))

	if o.entry != nil {
		status := glyphDownload + " download"
		if o.file != "" {
			status = "downloaded"
		}
		return fmt.Sprintf("%-8s %-*s", o.entry.OS, imageMetaWidth, o.entry.Variant) +
			size + "  " + status
	}
	osLabel := o.osName
	if osLabel == "" {
		osLabel = osUnknown
	}
	file := ansi.Truncate(o.file, byoFileWidth, "…")
	return fmt.Sprintf("%-8s %-*s %-*s", osLabel, imageMetaWidth, o.backend, byoFileWidth, file) +
		size + "  " + dimStyle.Render("byo")
}

// byoOptionFromPath turns a path the user browsed to into the same option
// shape localImageFiles produces for a file already in isos/. It must stay
// the same shape: everything downstream (backend/OS inference, and the ssh
// user resolution fixed in b6593b3) keys off entry == nil.
func byoOptionFromPath(path string) (imageOption, error) {
	st, err := os.Stat(path)
	if err != nil {
		return imageOption{}, err
	}
	if st.IsDir() {
		return imageOption{}, fmt.Errorf("%s is a directory", path)
	}
	backend, osName := iso.Infer(filepath.Base(path))
	return imageOption{
		file:    path,
		backend: backend,
		osName:  osName,
		bytes:   st.Size(),
		exact:   true,
	}, nil
}

// matchLocalImage is core.MatchLocal. The picker and core.Create must agree on
// which file a catalog entry means. The form now names the ENTRY and lets
// core resolve it, so two copies of this would let the picker offer one file
// and Create build on another.
var matchLocalImage = core.MatchLocal

// buildImages assembles the form's image picker from core.Images(): every
// catalog entry (in Catalog order), each flagged with whether it's already
// downloaded, then every local file that isn't claimed by a catalog entry,
// as BYO options. Matching a catalog entry to its local file and deciding
// whether the size shown is exact or the catalog's declared approximation is
// core's job now, not a second copy of it here (see core.Images' doc); this
// only decides how the picker PRESENTS what core reports.
//
// core.CatalogImage carries an ID but not the richer iso.Entry (Variant,
// SSHUser) the picker's rows and resolvedSSHUser want, so a downloaded-or-not
// catalog row is still paired with its iso.Entry by ID here. The error core.Images
// returns is not surfaced: LocalImages, underneath it, already swallows an
// unreadable isos/ dir as "no local files" rather than an error, so there is
// nothing left for a caller to react to.
func buildImages() []imageOption {
	imgs, _ := core.Images()
	catalog := map[string]iso.Entry{}
	for _, e := range iso.Catalog() {
		catalog[e.ID] = e
	}
	out := make([]imageOption, len(imgs))
	for i, ci := range imgs {
		if ci.ID == "" {
			out[i] = imageOption{file: ci.File, backend: ci.Backend, osName: ci.OS, bytes: ci.Bytes, exact: ci.Exact}
			continue
		}
		e := catalog[ci.ID]
		out[i] = imageOption{entry: &e, backend: e.Backend, osName: e.OS, sshUser: e.SSHUser,
			file: ci.File, bytes: ci.Bytes, exact: ci.Exact}
	}
	return out
}

// byoBackends is the fixed cycle offered on the fBackend override row.
var byoBackends = []string{"ssh", "apkovl", "cloudinit"}

// byoOSNames is the cycle offered on the fOS override row: every OS the
// catalog knows, in catalog order, led by "" meaning "whatever iso.Infer
// guessed".
//
// This row exists because Infer names an OS in exactly one case: a filename
// containing "alpine" that ends in .iso. Every qcow2, every img and every
// unrecognised file comes back with an empty OS, which flows into
// recipes.List, where both branches compare against a real OS name parsed off
// a recipe's filename. An empty OS matches nothing, so before this row a BYO
// image was offered NO recipes at all, ever. A hand-downloaded Ubuntu cloud
// image got none while the byte-identical catalog entry got xfce and
// devtools.
//
// Letting the user say so is the safe half of a choice the form already
// offers: fBackend lets them override the BACKEND guess, and getting that
// wrong yields an unbootable VM, where a wrong OS yields a recipe that fails
// loudly on apt-get. Permitting the dangerous override and forbidding the
// safe one was an oversight, not a guard.
func byoOSNames() []string {
	out := []string{""}
	for _, g := range iso.ByOS() {
		out = append(out, g.OS)
	}
	return out
}

// formContentWidth holds the new-vm pane at a constant width, so the box
// never resizes as optional rows appear.
//
// Sized off the HINT lines, which are the widest thing the form regularly
// shows. The mode hints are 44 and 45 cells, so at the old width of 56
// (a 44-cell value column) they wrapped by a single character and left a
// dangling word under every mode row. 60 cells of value column clears all
// of them but the cloud disk hint, which is 71 and would need a pane too
// wide to sit in an 80-column terminal.
//
// 72 plus the pane frame is 78, which is the point of it: the box still
// fits an 80-column terminal without paneAt having to clamp it.
const formContentWidth = 72

type formModel struct {
	inputs      []textinput.Model // name, ram, cpus, disk, share
	focus       int
	images      []imageOption
	imgIdx      int
	byoBackend  string // override for the selected BYO image's backend; "" means "use iso.Infer's guess"
	byoOS       string // override for the selected BYO image's OS; "" means "use iso.Infer's guess"
	mode        string // "live" | "disk"; meaningful only while the selected image's backend is apkovl
	err         string
	fetching    bool
	fetchingOS  string
	recipeNames []string        // installed recipes matching the selected image's OS/backend
	recipeIdx   int             // sub-cursor within the recipes row, moved by left/right
	recipeSel   map[string]bool // names currently checked
	// randomPassword swaps the fixed, documented console password for a
	// generated one. Cloud images only, see build().
	randomPassword bool
	dl             dlStats // last snapshot of the in-flight download

	// dlCtx/dlCancel are the in-flight fetch's ctx and its CancelFunc; nil
	// when nothing is running. esc calls dlCancel so the download actually
	// stops rather than merely being abandoned (core.DownloadImage's ctx
	// wiring is what makes that real; see fetchImage). dlCtx has no
	// production reader of its own: it is kept alongside dlCancel so a test
	// can check ctx.Err() after esc and confirm the cancellation was real,
	// not just that a flag flipped.
	dlCtx    context.Context
	dlCancel context.CancelFunc
	// dlGen is bumped every time a fetch starts or is cancelled. It is
	// stamped into imageFetchedMsg/imageFetchErrMsg so an outcome from a
	// fetch that has since been cancelled (the goroutine keeps running for
	// one more read after ctx is cancelled) is recognisable as stale and
	// dropped rather than reviving "fetching" or toasting a "context
	// canceled" the user did not cause.
	dlGen int
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
	fOS
	fRecipes
	fPassword
)

// focusOrder is the tab-traversal order of focus positions, which must match
// the visual order fields are rendered in by viewForm, not the order the
// field constants happen to be declared in.
type focusOrder []int

// order returns the tab-traversal order for the form's current state: name,
// image, [backend override, BYO only], [mode, apkovl only], ram, cpus,
// [disk, effective disk mode only], share, recipes. Conditional fields are
// omitted rather than included-but-hidden: viewForm doesn't render them in
// those states, so landing focus there would silently edit an invisible
// field (the same reasoning fDisk already followed pre-Task-8). recipes is
// always included, even with zero recipes matching: viewForm always renders
// that row (with a "none" placeholder), so it's always a valid landing spot.
func (f formModel) order() focusOrder {
	o := focusOrder{fName, fISO}
	if opt := f.selected(); opt != nil && opt.isBYO() {
		o = append(o, fBackend, fOS)
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

// resolvedOS is the OS build() will record and recipes.List will filter by:
// entry.OS for a catalog image, iso.Infer's guess for BYO unless overridden
// via fOS. Mirrors resolvedBackend.
func (f formModel) resolvedOS() string {
	opt := f.selected()
	if opt == nil {
		return ""
	}
	if opt.isBYO() && f.byoOS != "" {
		return f.byoOS
	}
	return opt.osName
}

func (f formModel) resolvedSSHUser() string {
	opt := f.selected()
	if opt == nil {
		return ""
	}
	// The cloud-init seed creates exactly one account, cloudinit.User, so
	// anything provisioned through that backend connects as it, including a
	// BYO file the user has just declared to be a cloud image via fBackend.
	// Left to fall through, a BYO image would record an empty SSHUser, sshx
	// would default that to root, and cloud images lock root: ssh and
	// provisioning would both fail on a VM that looked correctly configured.
	// Catalog cloud entries already carry this same user, so this changes
	// nothing for them.
	if f.resolvedBackend() == "cloudinit" {
		return cloudinit.User
	}
	return opt.sshUser
}

// effectiveMode is the Mode build() will write. cloudinit is always "cloud"
// (a cloud image boots straight off its overlay, no install step); ssh is
// always "disk" (an unrecognised BYO file is assumed to need a real install,
// then manual/ssh provisioning; the apkovl live path only exists for
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
// image's OS/backend and clears any selection made against the old list:
// a recipe name from one OS is meaningless once the picker moves to another.
func (f *formModel) refreshRecipes() {
	f.recipeNames, _ = recipes.List(f.resolvedOS(), f.resolvedBackend())
	f.recipeSel = map[string]bool{}
	f.recipeIdx = 0
}

// selectImage adopts the image at idx, along with everything that has to move
// with it. Picking an image is not just an index: the BYO backend and OS
// overrides belonged to the previous image and would otherwise silently apply
// to this one, and the recipe list is filtered by the image's OS and backend,
// so a stale list would offer recipes that cannot run on what is now selected.
//
// Out-of-range is ignored rather than clamped: every caller derives idx from
// the image slice itself, so a bad index means a bug elsewhere, and clamping
// would select an arbitrary image instead of making that visible.
func (f *formModel) selectImage(idx int) {
	if idx < 0 || idx >= len(f.images) {
		return
	}
	f.imgIdx = idx
	f.byoBackend = ""
	f.byoOS = ""
	f.refreshRecipes()
}

func newForm() formModel {
	f := formModel{mode: "live", recipeSel: map[string]bool{}}
	labels := []string{"work", "4096", "4", "8G", "~/vms"}
	for i := 0; i < fieldCount; i++ {
		ti := theme.TextInput()
		ti.SetValue(labels[i])
		f.inputs = append(f.inputs, ti)
	}
	f.inputs[fName].SetValue("")
	f.inputs[fName].Placeholder = "name"
	// An explicit width is required, not cosmetic: bubbles v2.1.1 sizes an
	// internal buffer to Width()+1 when rendering a placeholder, so with the
	// width unset the placeholder is cut to its first rune ("name" -> "n").
	// Only safe on rows that append nothing after the input: a width also
	// pads the VALUE out to it, which would push the " MB" after ram far right.
	f.inputs[fName].SetWidth(formContentWidth - fieldValueColumn)
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

// imageFetchedMsg and imageFetchErrMsg both carry gen, the generation the
// fetch was started under. A cancelled fetch's underlying goroutine (see
// core.DownloadImage) does not stop instantly, so its outcome can still
// arrive after esc has already moved dlGen on; updateForm compares gen
// against the CURRENT m.form.dlGen and drops anything that doesn't match,
// rather than reviving a "fetching" state or a "context canceled" toast the
// user did not ask to see.
type imageFetchedMsg struct {
	entryID string
	gen     int
	// unverified is true when no published checksum existed to check the
	// download against. The user is about to boot these bytes.
	unverified bool
}
type imageFetchErrMsg struct {
	entryID string
	err     string
	gen     int
}

// fetchImage downloads catalog entry id, reporting progress through dlRecord
// exactly as before. Re-running it on an image that is already local is safe
// and is how "re-download" works: core.DownloadImage's checksum check only
// short-circuits when the local file's digest MATCHES the published one, so
// a truncated or superseded file mismatches and is refetched in full.
//
// ctx is the caller's to cancel. Unlike the direct iso.Resolve/iso.Download
// call this replaces, core.DownloadImage builds its request from ctx, so
// cancelling it (updateForm's esc handler) genuinely stops the transfer
// instead of abandoning a goroutine that keeps writing.
func fetchImage(ctx context.Context, id string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := core.DownloadImage(ctx, id, dlRecord)
		if err != nil {
			return imageFetchErrMsg{entryID: id, err: err.Error(), gen: gen}
		}
		return imageFetchedMsg{entryID: id, gen: gen, unverified: !res.ChecksumAvailable}
	}
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dlTickMsg:
		// Anchored to m.form.fetching, not dlGen: the tick chain only needs
		// to know whether ANY fetch is live, not which one, and fetching is
		// cleared the instant esc cancels, so the chain stops re-arming right
		// away rather than ticking a bar for a download that no longer
		// exists.
		if !m.form.fetching {
			return m, nil
		}
		m.form.dl = dlSnapshot(time.Now())
		return m, dlTick()

	case imageFetchedMsg:
		if msg.gen != m.form.dlGen {
			return m, nil // outcome of a fetch esc already cancelled
		}
		m.form.fetching = false
		m.form.dlCtx, m.form.dlCancel = nil, nil
		m.form.images = buildImages()
		path := ""
		for i, opt := range m.form.images {
			if opt.entry != nil && opt.entry.ID == msg.entryID {
				m.form.imgIdx = i
				path = "isos/" + opt.file
			}
		}
		m.form.refreshRecipes()
		msgText := "downloaded " + path
		if msg.unverified {
			msgText += ": UNVERIFIED (no published checksum)"
		}
		cmd := m.showToast(msgText, msg.unverified)
		return m, cmd

	case imageFetchErrMsg:
		if msg.gen != m.form.dlGen {
			return m, nil // outcome (including the cancellation itself) of a fetch esc already cancelled
		}
		m.form.fetching = false
		m.form.dlCtx, m.form.dlCancel = nil, nil
		cmd := m.showToast(m.form.fetchingOS+": "+msg.err, true)
		return m, cmd

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			// core.DownloadImage builds its request from ctx, so cancelling
			// it here actually stops the transfer rather than abandoning a
			// goroutine that keeps writing (the bug this used to be). dlGen
			// is bumped so the abandoned goroutine's eventual outcome
			// message, stamped with the OLD generation, is recognised as
			// stale and dropped instead of reviving "fetching".
			if m.form.fetching {
				m.form.dlGen++
				if m.form.dlCancel != nil {
					m.form.dlCancel()
					m.form.dlCtx, m.form.dlCancel = nil, nil
				}
				m.form.fetching = false
			}
			m.screen = screenList
			m.status = ""
			m.showHelp = false
			return m, nil
		case "?":
			// Only a help toggle when no text field has focus. Otherwise "?"
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
				// Opens the picker rather than cycling in place. Cycling
				// modelled one dimension, and the catalog now has two: alpine
				// ships standard and virt, which differ only in build, so a
				// flat cycle gave no way to see that a second Alpine even
				// existed, let alone which one was under the cursor.
				//
				// enter is not the opening key: it creates the VM everywhere
				// else in this form, and a key that submits on eight rows and
				// opens a picker on the ninth is a trap.
				if len(m.form.images) == 0 {
					return m, nil
				}
				m.modal = newImageModal(m.form.images, m.form.imgIdx)
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
			case fOS:
				names := byoOSNames()
				cur := m.form.resolvedOS()
				idx := 0
				for i, o := range names {
					if o == cur {
						idx = i
					}
				}
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				m.form.byoOS = names[(idx+d+len(names))%len(names)]
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
		case keySpace:
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
					cmd := m.showToast("byo images are already local, nothing to download", true)
					return m, cmd
				}
				if m.form.fetching {
					return m, nil // a fetch is already in flight; don't start a second one
				}
				m.form.fetching = true
				m.form.fetchingOS = opt.entry.OS
				m.form.dl = dlStats{}
				m.status = ""
				m.form.dlGen++
				ctx, cancel := context.WithCancel(context.Background())
				m.form.dlCtx, m.form.dlCancel = ctx, cancel
				dlStart()
				return m, tea.Batch(fetchImage(ctx, opt.entry.ID, m.form.dlGen), dlTick())
			}
		case "enter":
			s, err := m.form.spec()
			if err == nil {
				// Plan is the same validation Create runs, so the form can put
				// the error next to the fields that caused it instead of
				// letting it arrive later as a toast over the list.
				_, err = core.Plan(s)
			}
			if err != nil {
				m.form.err = err.Error()
				return m, nil
			}
			return m, tea.Sequence(createVM(s), loadVMs, backToList())
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

// spec turns the form into a core.Spec. Everything that decides what the VM
// IS (image resolution, OS, backend, ssh user, mode, defaults, validation)
// lives in core, so the CLI and an MCP server get the same VM the form does.
// What stays here is the two checks only the form can make, about its own
// picker rather than about the VM.
func (f formModel) spec() (core.Spec, error) {
	opt := f.selected()
	if opt == nil {
		return core.Spec{}, fmt.Errorf("pick an image first")
	}
	if opt.file == "" {
		return core.Spec{}, fmt.Errorf("press space to download %s first", opt.entry.OS)
	}
	// A catalog image is named by its ID, not by the path the picker resolved
	// it to: core re-runs the same MatchLocal to find the file, and keeping the
	// ID is what lets it read the ENTRY's backend, OS and ssh user rather than
	// re-inferring them from the filename. Inference is a good guess for a file
	// nobody has described; it is strictly worse than an entry that states the
	// answer. A BYO file has no entry, so it goes by path.
	var image string
	var err error
	if opt.entry != nil {
		image = opt.entry.ID
	} else if image, err = opt.imagePath(); err != nil {
		return core.Spec{}, err
	}

	selected := []string{}
	for _, r := range f.recipeNames {
		if f.recipeSel[r] {
			selected = append(selected, r)
		}
	}
	// The form's numeric fields are free text, so a non-number has to become
	// something core will reject rather than silently reading as 0, which core
	// treats as "use the default".
	ram, err := strconv.Atoi(strings.TrimSpace(f.inputs[fRAM].Value()))
	if err != nil {
		return core.Spec{}, fmt.Errorf("ram must be a number of MB, at least 256")
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(f.inputs[fCPUs].Value()))
	if err != nil {
		return core.Spec{}, fmt.Errorf("cpus must be at least 1")
	}

	s := core.Spec{
		Name:    strings.TrimSpace(f.inputs[fName].Value()),
		Image:   image,
		OS:      f.byoOS,
		Backend: f.byoBackend,
		Mode:    f.effectiveMode(),
		RAM:     ram,
		CPUs:    cpus,
		Disk:    strings.TrimSpace(f.inputs[fDisk].Value()),
		Share:   strings.TrimSpace(f.inputs[fShare].Value()),
		Recipes: selected,
	}
	if f.randomPassword {
		s.ConsolePassword = "random"
	}
	return s, nil
}

// createVM is the tea.Cmd wrapper around core.Create.
func createVM(s core.Spec) tea.Cmd {
	return func() tea.Msg {
		v, err := core.Create(s)
		if err != nil {
			return errMsg(err.Error())
		}
		return statusMsg("created " + v.Name)
	}
}

func (m model) viewForm() string {
	f := m.form
	b := fields{width: formContentWidth}

	group := b.gap

	row := func(i int, label, value string) {
		marker := "  "
		if f.focus == i {
			marker = selStyle.Render(glyphCursor)
			// Text inputs are NOT wrapped: a textinput's view carries its own
			// cursor styling, and a styled substring's \x1b[0m resets the
			// enclosing style too, so wrapping produced a row that was accent
			// up to the cursor and default after it. The ❯ and the input's own
			// cursor already mark focus. Picker rows are plain strings and wrap
			// safely.
			if i >= fieldCount {
				value = selStyle.Render(value)
			}
		}
		b.row(marker, label, value)
	}

	row(fName, "name", f.inputs[fName].View())
	group()

	opt := f.selected()
	imgLabel := "- (none)"
	if opt != nil {
		imgLabel = opt.label()
	}
	row(fISO, "image", imgLabel)

	if opt != nil && opt.isBYO() {
		row(fBackend, "backend", f.resolvedBackend())
		// A BYO file's OS is almost never inferable from its name, and an
		// unset one silently means "no recipes at all", so the row says
		// "unknown" rather than rendering blank, and the hint says what
		// setting it buys.
		osValue := f.resolvedOS()
		if osValue == "" {
			osValue = dimStyle.Render(osUnknown)
		}
		row(fOS, "os", osValue)
		if f.resolvedOS() == "" {
			b.hint("set the os to be offered recipes for it")
		}
	}

	if f.resolvedBackend() == "apkovl" {
		modeRow := radio(modeLabel("live"), f.mode == "live") + "   " + radio(modeLabel("disk"), f.mode == "disk")
		row(fMode, "mode", modeRow)
	} else {
		// Not a field: with a non-apkovl image the mode is implied by the
		// image, so it is shown as a fact rather than something to focus.
		b.row("", "mode", dimStyle.Render(modeLabel(f.effectiveMode())))
	}
	b.hint(modeHint(f.effectiveMode()))

	group()

	row(fRAM, "ram", f.inputs[fRAM].View()+dimStyle.Render(" MB"))
	row(fCPUs, "cpus", f.inputs[fCPUs].View())
	switch f.effectiveMode() {
	case "disk":
		row(fDisk, "disk", f.inputs[fDisk].View())
	case "cloud":
		row(fDisk, "disk", f.inputs[fDisk].View())
		b.hint("cloud images ship ~2.4G of usable root, raise this to install anything")
	default:
		b.row("", "disk", dimStyle.Render("- (live mode)"))
	}
	group()

	row(fShare, "share", f.inputs[fShare].View())

	recipesMarker := "  "
	if f.focus == fRecipes {
		recipesMarker = selStyle.Render(glyphCursor)
	}
	b.row(recipesMarker, "recipes", f.recipesLabel())

	if f.resolvedBackend() == "cloudinit" {
		marker := "  "
		if f.focus == fPassword {
			marker = selStyle.Render(glyphCursor)
		}
		b.row(marker, "console", radio("stoat", !f.randomPassword)+"  "+radio("random", f.randomPassword))
		// cloudinit is always cloud mode (effectiveMode above), and a cloud VM
		// never gets a QEMU window (qemu.NeedsWindow), so this password is only
		// ever typed at the VNC socket the detail screen surfaces.
		b.hint("stoat user's login over VNC, cloud VMs have no qemu window")
	}

	// The download block and the error are full-width blocks, not field rows,
	// so they sit under the table rather than inside it.
	body := b.String()
	if f.fetching {
		body += "\n\n" + dlView(f.fetchingOS, f.dl)
	}
	if f.err != "" {
		body += "\n\n" + errStyle.Render(f.err)
	}

	box := paneAt("new vm", body, formContentWidth, m.width)

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
		return dimStyle.Render("- (no matching recipes)")
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
	return wrapItems(items, formContentWidth-fieldValueColumn, strings.Repeat(" ", fieldValueColumn))
}
