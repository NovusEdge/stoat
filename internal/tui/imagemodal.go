package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The image picker is two levels — choose an OS, then a variant within it —
// because the catalog stopped having exactly one entry per OS. Alpine ships
// both standard and virt, which differ only in build (66 MiB virtio-only vs
// 352 MiB), and a flat row cycling with left/right gave the user no way to
// tell why there were suddenly two Alpines, let alone which to want.
//
// It is a modal rather than another form row because the second level needs
// its own cursor. A row that cycles can only ever express one dimension.

// modalLevel is which of the two lists the modal is currently showing.
type modalLevel int

const (
	levelOS modalLevel = iota
	levelVariant
)

// byoGroup is the OS name given to bring-your-own images, which have no
// catalog OS of their own. It is a real group rather than a special case so
// the second level can list several BYO files under one heading.
const byoGroup = "byo"

// osItem is a level-one row: an OS, and the indices into the form's image
// slice that belong to it.
type osItem struct {
	os   string
	idxs []int
	only imageOption // the sole image, when len(idxs) == 1; zero otherwise
}

func (i osItem) FilterValue() string { return i.os }

// variantItem is a level-two row: one concrete image. idx is its position in
// the form's image slice — carried on the item rather than inferred from the
// cursor, because bubbles/list indexes into its FILTERED view and an index
// taken from the cursor would point at the wrong image the moment a filter is
// applied.
type variantItem struct {
	idx    int
	opt    imageOption
	browse bool // the byo group's trailing leaf: opens the filepicker instead of selecting opt
	find   bool // the byo group's other leaf: opens the fuzzy finder instead of selecting opt
}

func (i variantItem) FilterValue() string {
	switch {
	case i.find:
		return "find…"
	case i.browse:
		return "browse…"
	default:
		return i.opt.variantLabel()
	}
}

// foundItem is one row of the fuzzy finder. FilterValue is the FULL path, not
// the base name, so a query can narrow by directory ("dl alp") as well as by
// file name -- which is the main thing that makes a flat list of every image
// on the machine usable.
type foundItem struct{ img foundImage }

func (i foundItem) FilterValue() string { return i.img.path }

// variantLabel is what distinguishes this image from the others sharing its
// OS: the catalog's own variant label, or the filename for a BYO file, which
// is the only thing there is to tell two of those apart by.
//
// BYO filenames are truncated because they are the one label here with no
// bound — ubuntu-24.04-server-cloudimg-amd64.img is 38 cells against a
// 40-cell box — and an overlong one would push the modal's border out of
// square rather than simply reading long.
func (o imageOption) variantLabel() string {
	if o.entry != nil {
		return o.entry.Variant
	}
	return ansi.Truncate(o.file, modalVariantWidth, "…")
}

// modalVariantWidth is the label column inside the modal: the box's content
// width less the cursor, the size column and the status that follow it.
const modalVariantWidth = 20

// statusLabel says whether the image is on disk yet. A BYO file is local by
// definition — that is what makes it BYO — so it reports no download state.
func (o imageOption) statusLabel() string {
	if o.entry == nil {
		return dimStyle.Render("local")
	}
	if o.file != "" {
		return dimStyle.Render("downloaded")
	}
	return glyphDownload + " download"
}

// imageDelegate renders both levels. One delegate rather than two because
// the modal swaps items in place on the same list, exactly as vmDelegate
// covers both good and broken VMs on one cursor.
type imageDelegate struct{}

func (imageDelegate) Height() int  { return 1 }
func (imageDelegate) Spacing() int { return 0 } // compact: the modal is a menu, not the VM list

func (imageDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d imageDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	cursor := "  "
	if index == m.Index() {
		cursor = selStyle.Render(glyphCursor)
	}

	switch it := item.(type) {
	case osItem:
		// A group with several variants shows the count, because that is what
		// says there is a second Alpine to choose between. A group with one
		// shows its size instead: "1 image" tells the user nothing, and a
		// single-variant OS resolves straight from this level, so this is the
		// only place its size would ever be seen.
		trailer := fmt.Sprintf("%d images", len(it.idxs))
		switch {
		case len(it.idxs) == 1:
			trailer = it.only.sizeLabel()
		case it.os == byoGroup && len(it.idxs) == 0:
			// No BYO file has been found under isos/ yet, but the group still
			// has to be selectable — it's the only route to browse….
			trailer = "browse…"
		}
		label := fmt.Sprintf("%-*s", modalVariantWidth, it.os)
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		fmt.Fprint(w, cursor+label+dimStyle.Render(trailer))
	case variantItem:
		if it.find {
			// No size or status column: it isn't a file, it's a door to the
			// finder, so those columns would just be blank.
			label := fmt.Sprintf("%-*s", modalVariantWidth, "find…")
			if index == m.Index() {
				label = selStyle.Render(label)
			}
			fmt.Fprint(w, cursor+label+dimStyle.Render("search by name"))
			return
		}
		if it.browse {
			// No size or status column: it isn't a file, it's a door to the
			// filepicker, so those columns would just be blank.
			label := fmt.Sprintf("%-*s", modalVariantWidth, "browse…")
			if index == m.Index() {
				label = selStyle.Render(label)
			}
			fmt.Fprint(w, cursor+label+dimStyle.Render("choose a file"))
			return
		}
		// Styled substrings end in \x1b[0m, which resets the ENCLOSING style
		// too — so the status stays outside the selection wrap, or a
		// highlighted row would render everything after it unhighlighted.
		// Padded plain and styled afterwards, for the same reason.
		label := fmt.Sprintf("%-*s", modalVariantWidth, it.opt.variantLabel())
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		// Size right-aligned so the digits line up column-wise; padded plain,
		// then dimmed as a whole segment.
		size := dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, it.opt.sizeLabel()))
		fmt.Fprint(w, cursor+label+size+"  "+it.opt.statusLabel())
	case foundItem:
		// Base name, then the parent directory dimmed, then the size --
		// matching the columns the tree browser shows (ShowSize = true). The
		// directory is what tells apart two files that share a name. Widths
		// sum to modalContentWidth so the row never wraps.
		name := ansi.Truncate(filepath.Base(it.img.path), foundNameWidth, "…")
		label := fmt.Sprintf("%-*s", foundNameWidth, name)
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		dirText := ansi.Truncate(filepath.Dir(it.img.path), foundDirWidth, "…")
		dir := dimStyle.Render(fmt.Sprintf("%-*s", foundDirWidth, dirText))
		size := dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, humanBytes(it.img.size)))
		fmt.Fprint(w, cursor+label+dir+size)
	}
}

// modalContentWidth is the box's inner width, and modalRows the most rows it
// shows before paginating. Both are sized to fit the smallest terminal stoat
// renders panes in at all (smallWidth x smallHeight, 60x20): the box comes to
// modalContentWidth+paneFrame() wide and modalRows+6 tall, which leaves room
// at that size rather than assuming the developer's window.
const (
	modalContentWidth = 46
	modalRows         = 6
)

// modalSizeWidth is the size column, right-aligned so the digits line up down
// the list — the whole point of showing sizes is comparing them, and a ragged
// left edge makes that harder than it needs to be.
//
// Nine, not eight: humanBytes keeps one decimal below 100 ("66.0 MiB"), so the
// widest value is "~66.0 MiB" rather than the "~595 MiB" you would guess from
// the biggest image. Sized to eight, the status column shifted a cell on
// exactly the rows with a small image — which is alpine-virt, the row this
// whole feature exists to make comparable.
const modalSizeWidth = 9

// foundNameWidth and foundDirWidth are the finder's two text columns. Their
// sum plus the cursor and modalSizeWidth equals modalContentWidth exactly, so
// a found row never wraps the way the other rows' fixed columns don't either.
const (
	foundNameWidth = 20
	foundDirWidth  = modalContentWidth - 2 - foundNameWidth - modalSizeWidth
)

// imageModal is the two-level picker. One list, re-populated on drill-down,
// rather than two lists: two would mean two cursors to keep consistent and
// two things to size against the terminal.
type imageModal struct {
	list   list.Model
	level  modalLevel
	groups []osItem
	images []imageOption
	osName string // the group drilled into; meaningful at levelVariant

	// browsing and picker back the byo group's browse… leaf. A third level
	// rather than a variantItem that opens something of its own: the picker
	// needs its own key routing (see update/updateBrowsing), and folding that
	// into the variant-level switch would make it responsible for two
	// different kinds of input.
	browsing bool
	picker   filepicker.Model

	// finding is the fuzzy finder, the byo group's other leaf. It is a
	// separate sub-mode from browsing for the same reason browsing is one:
	// it owns the keyboard while open (typing is a filter, not navigation).
	finding       bool
	findList      list.Model
	findListReady bool // whether findList has been built by ensureFindList
	// scanCh is the channel the running scan streams batches down. Stored so
	// updateFinding can re-issue waitForImages after each batch; openFinder's
	// local ch goes out of scope the moment it returns.
	scanCh <-chan []foundImage
	// scanCancel, closed by stopScan, tells the scan goroutine to give up
	// rather than block forever on a send nobody will read (see scanImages).
	scanCancel chan struct{}
	// scanGen is the current scan's generation, stamped into every
	// imagesFoundMsg openFinder issues. updateFinding drops any message whose
	// generation doesn't match -- see imagesFoundMsg's doc.
	scanGen int
	// scanDone distinguishes "found nothing yet" from "found nothing" -- an
	// empty pane means opposite things before and after the walk ends.
	scanDone bool
}

// newImageList builds the modal's list component. Filtering is deliberately
// off: the whole catalog is five OSes, so a search box would earn nothing,
// and it would make esc ambiguous — the key has to mean "go back a level"
// here, and bubbles/list claims it for clearing a filter.
func newImageList() list.Model {
	l := list.New(nil, imageDelegate{}, modalContentWidth, modalRows)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.Styles = listStyles(l.Styles)
	return l
}

// listStyles is the styling shared by both of the modal's lists, applied on
// top of whatever list.New already set, so the OS picker and the finder
// cannot drift apart visually.
func listStyles(s list.Styles) list.Styles {
	s.NoItems = dimStyle
	s.PaginationStyle = lipgloss.NewStyle()
	s.ActivePaginationDot = accentStyle
	s.InactivePaginationDot = dimStyle
	return s
}

// groupImages buckets the form's images by OS, preserving the order they
// arrive in — that order is the catalog's own, which is hand-written with the
// most generally useful image first, and sorting would replace that judgement
// with alphabetical noise. BYO files land in one trailing group.
func groupImages(images []imageOption) []osItem {
	var groups []osItem
	at := map[string]int{}
	group := func(name string) int {
		j, seen := at[name]
		if !seen {
			at[name] = len(groups)
			groups = append(groups, osItem{os: name})
			j = len(groups) - 1
		}
		return j
	}
	for i, o := range images {
		name := o.osName
		if o.isBYO() {
			name = byoGroup
		}
		if name == "" {
			// iso.Infer could not name the OS of a BYO file; it still has to
			// be selectable, so it joins the byo group rather than forming a
			// nameless one.
			name = byoGroup
		}
		j := group(name)
		groups[j].idxs = append(groups[j].idxs, i)
	}
	// byo always gets a group, even with zero files under isos/ yet: browse…
	// lives inside it, and browsing for the first BYO image has to work
	// before there is one on disk to have grouped above.
	group(byoGroup)
	// A one-image group carries that image, so the first level can show its
	// size — it is the only level a single-variant OS is ever seen at.
	for i := range groups {
		if len(groups[i].idxs) == 1 {
			groups[i].only = images[groups[i].idxs[0]]
		}
	}
	return groups
}

// newImageModal opens the picker at the OS level, with the cursor on the
// group owning the currently selected image, so opening it does not lose the
// user's place.
func newImageModal(images []imageOption, current int) *imageModal {
	mo := &imageModal{list: newImageList(), images: images, groups: groupImages(images)}
	mo.showOSLevel()
	for i, g := range mo.groups {
		for _, idx := range g.idxs {
			if idx == current {
				mo.list.Select(i)
			}
		}
	}
	return mo
}

// showOSLevel populates the list with the OS groups.
func (mo *imageModal) showOSLevel() {
	items := make([]list.Item, 0, len(mo.groups))
	for _, g := range mo.groups {
		items = append(items, g)
	}
	mo.level = levelOS
	mo.osName = ""
	mo.list.SetItems(items)
	mo.syncHeight(len(items))
}

// drill switches to the variants of one group.
func (mo *imageModal) drill(g osItem) {
	items := make([]list.Item, 0, len(g.idxs)+1)
	for _, idx := range g.idxs {
		items = append(items, variantItem{idx: idx, opt: mo.images[idx]})
	}
	if g.os == byoGroup {
		// find… before browse…: typing a name is the common case, walking the
		// tree is the fallback. Trailing rather than leading relative to the
		// real files: existing BYO files are the images the user most likely
		// wants, so these two are the fallback at the bottom, not the first
		// thing the cursor lands on.
		items = append(items, variantItem{find: true}, variantItem{browse: true})
	}
	mo.level = levelVariant
	mo.osName = g.os
	mo.list.SetItems(items)
	mo.list.Select(0)
	mo.syncHeight(len(items))
}

// syncHeight shrinks the list to the rows it actually has, so a two-item
// level does not draw a box with four blank lines in it — bubbles/list pads
// its viewport to whatever height it is given.
func (mo *imageModal) syncHeight(n int) {
	if n > modalRows {
		n = modalRows
	}
	if n < 1 {
		n = 1
	}
	mo.list.SetShowPagination(len(mo.list.Items()) > modalRows)
	mo.list.SetHeight(n)
}

// update handles one message while the modal is open. It returns the index of
// the chosen image (-1 when nothing was chosen yet) and whether the modal
// should close.
//
// ctrl+c is deliberately absent: app.go handles it centrally before the
// message switch, and duplicating it per sub-mode is exactly how it has
// regressed before.
func (mo *imageModal) update(msg tea.Msg) (tea.Cmd, int, bool) {
	if mo.browsing {
		return mo.updateBrowsing(msg)
	}
	if mo.finding {
		return mo.updateFinding(msg)
	}

	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		mo.list, cmd = mo.list.Update(msg)
		return cmd, -1, false
	}

	switch key.String() {
	case "esc", "left":
		// Back a level, not straight out: at the variant level the user is
		// one step into a choice, and dumping them back to the form would
		// throw away the OS they already picked.
		if mo.level == levelVariant {
			mo.showOSLevel()
			for i, g := range mo.groups {
				if g.os == mo.osName {
					mo.list.Select(i)
				}
			}
			return nil, -1, false
		}
		return nil, -1, true
	case "enter", "right":
		switch mo.level {
		case levelOS:
			g, ok := mo.list.SelectedItem().(osItem)
			if !ok {
				return nil, -1, false
			}
			// A group with one image has nothing to choose between; drilling
			// in to press enter again would be a keystroke for no decision.
			// byo is exempt even at exactly one: it always drills, because
			// that one image sits alongside browse…, which the shortcut
			// would otherwise make unreachable.
			if len(g.idxs) == 1 && g.os != byoGroup {
				return nil, g.idxs[0], true
			}
			mo.drill(g)
			return nil, -1, false
		default:
			it, ok := mo.list.SelectedItem().(variantItem)
			if !ok {
				return nil, -1, false
			}
			if it.find {
				return mo.openFinder(), -1, false
			}
			if it.browse {
				return mo.openBrowser(), -1, false
			}
			return nil, it.idx, true
		}
	}

	var cmd tea.Cmd
	mo.list, cmd = mo.list.Update(msg)
	return cmd, -1, false
}

// openBrowser swaps the variant list for a file picker. AutoHeight is off
// because the modal owns the rectangle — a self-sizing component fights the
// layout, same rule as every other pane. The returned cmd is filepicker's own
// Init(), which kicks off the directory read; without it the picker would sit
// showing "no files" forever; nothing else triggers that first read.
func (mo *imageModal) openBrowser() tea.Cmd {
	p := filepicker.New()
	p.AllowedTypes = []string{".iso", ".qcow2", ".img"}
	p.ShowSize = true
	p.AutoHeight = false
	p.DirAllowed = false
	p.FileAllowed = true
	p.Cursor = glyphCursor
	p.Styles = filepickerStyles()
	p.SetHeight(modalRows)
	mo.picker = p
	mo.browsing = true
	return p.Init()
}

// filepickerStyles draws the picker in stoat's palette rather than bubbles'
// stock magenta-and-grey, so it reads as one more pane of this app instead of
// a different program spliced in.
func filepickerStyles() filepicker.Styles {
	s := filepicker.DefaultStyles()
	s.Cursor = selStyle
	s.Selected = selStyle
	s.Directory = accentStyle
	s.File = lipgloss.NewStyle()
	s.Symlink = accentStyle
	s.Permission = dimStyle
	s.FileSize = dimStyle
	s.DisabledFile = dimStyle
	s.DisabledSelected = dimStyle
	s.DisabledCursor = dimStyle
	s.EmptyDirectory = dimStyle.SetString("no matching files")
	return s
}

// updateBrowsing routes messages to the picker while it is open in place of
// the variant list. It handles both keys and the picker's own non-key
// messages (its directory read arrives as one), unlike the top-level update,
// which only ever sees key presses — app.go widens that gate to include
// non-key messages for exactly as long as mo.browsing is true.
func (mo *imageModal) updateBrowsing(msg tea.Msg) (tea.Cmd, int, bool) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "esc" {
		// Back to the variant list, not out of the modal — esc means "back a
		// level" everywhere else here, and closing outright on it would throw
		// away the group the user already drilled into.
		mo.browsing = false
		return nil, -1, false
	}

	var cmd tea.Cmd
	mo.picker, cmd = mo.picker.Update(msg)

	if didSelect, path := mo.picker.DidSelectFile(msg); didSelect {
		opt, err := byoOptionFromPath(path)
		if err != nil {
			// Stat can fail between the picker listing the file and the user
			// selecting it (deleted, unmounted). Stay open rather than close
			// the modal on a choice that didn't actually resolve.
			return cmd, -1, false
		}
		// The new option isn't in the slice the modal was opened with — it
		// came from anywhere on disk, that's the whole feature — so it's
		// appended here and returned as an index past the caller's original
		// bounds. The caller (app.go) re-adopts mo.images before resolving
		// that index; see the comment there.
		mo.images = append(mo.images, opt)
		mo.browsing = false
		return cmd, len(mo.images) - 1, true
	}

	return cmd, -1, false
}

// openFinder swaps the variant list for a fuzzy search over every disk image
// under $HOME. Filtering is enabled here and nowhere else: the OS and variant
// lists are short, curated and meant to be arrowed through, while this one is
// every image on the machine and is only usable by typing.
//
// The returned cmd pumps the first batch; each imagesFoundMsg re-issues it
// until the channel closes. Without it the scan runs and nothing ever reads
// the results.
func (mo *imageModal) openFinder() tea.Cmd {
	mo.ensureFindList()
	// Cleared, not appended to: re-entering the finder starts a fresh scan,
	// and the old scan's results are still sitting in findList's items from
	// last time. Without this every re-entry doubles the whole list.
	mo.findList.SetItems(nil)
	mo.stopScan() // in case the previous scan is somehow still running
	mo.finding = true
	mo.scanDone = false
	mo.scanGen++

	cancel := make(chan struct{})
	mo.scanCancel = cancel
	ch := scanImages(homeDir(), cancel)
	mo.scanCh = ch
	return waitForImages(ch, mo.scanGen)
}

// stopScan tells the running scan's goroutine to give up rather than block
// forever on a batch nobody will read, and forgets the channel so a stray
// repump can't be issued against it. Called whenever the finder is left --
// esc, choosing an image, or the modal closing outright.
func (mo *imageModal) stopScan() {
	if mo.scanCancel != nil {
		close(mo.scanCancel)
		mo.scanCancel = nil
	}
	mo.scanCh = nil
}

// ensureFindList lazily builds the finder's list on first use. It is
// separate from openFinder so setFound and view work correctly even when a
// caller sets mo.finding directly rather than going through the scan (as the
// tests do), without requiring a live scan to see the finder at all.
func (mo *imageModal) ensureFindList() {
	if mo.findListReady {
		return
	}
	// Height is modalRows content rows PLUS one: bubbles/list reserves a
	// header row whenever filtering is enabled and shown (it doubles as the
	// filter prompt while typing), even with the title itself off. Without
	// the +1 that row eats into the item rows instead of the blank space
	// above them.
	l := list.New(nil, imageDelegate{}, modalContentWidth, modalRows+1)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.Styles = listStyles(l.Styles)
	mo.findList = l
	mo.findListReady = true
}

// setFound appends a batch and re-sorts. Sorting on every batch rather than
// once at the end keeps the visible order stable while results stream in --
// rows that jump around under a cursor the user is already moving are worse
// than a slightly late sort.
func (mo *imageModal) setFound(found []foundImage) {
	mo.ensureFindList()
	items := mo.findList.Items()
	for _, f := range found {
		items = append(items, foundItem{img: f})
	}
	sort.SliceStable(items, func(a, b int) bool {
		return items[a].(foundItem).img.path < items[b].(foundItem).img.path
	})
	mo.findList.SetItems(items)
	mo.findList.SetShowPagination(len(items) > modalRows)
}

// updateFinding owns the keyboard while the finder is open. Keys go to the
// list first EXCEPT esc and enter: the list would swallow esc to clear its
// filter and enter to apply it, and neither is what those keys mean here.
func (mo *imageModal) updateFinding(msg tea.Msg) (tea.Cmd, int, bool) {
	mo.ensureFindList()
	if found, ok := msg.(imagesFoundMsg); ok {
		if found.gen != mo.scanGen {
			// A message from an abandoned scan (esc, or re-entering the
			// finder since it was issued). Dropped outright: repumping it
			// would read the WRONG channel, and a stale done would mark the
			// NEW scan finished while it is still running.
			return nil, -1, false
		}
		if found.done {
			mo.scanDone = true
			return nil, -1, false
		}
		mo.setFound(found.batch)
		// Pump the next batch. The scan is still running.
		if mo.scanCh == nil {
			return nil, -1, false
		}
		return waitForImages(mo.scanCh, mo.scanGen), -1, false
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			// Back a level, not out -- same rule as updateBrowsing. When a
			// filter is active esc clears it first, which is what the user
			// means by esc while they are still typing.
			if mo.findList.FilterState() != list.Unfiltered {
				break // let the list clear its own filter
			}
			mo.stopScan()
			mo.finding = false
			return nil, -1, false
		case "enter":
			// While the filter input is open, enter applies the filter --
			// that is the list's own interaction and must not be stolen, or
			// the user can never commit a query.
			if mo.findList.FilterState() == list.Filtering {
				break
			}
			it, ok := mo.findList.SelectedItem().(foundItem)
			if !ok {
				return nil, -1, false
			}
			opt, err := byoOptionFromPath(it.img.path)
			if err != nil {
				// Deleted or unmounted since the scan listed it. Stay open
				// rather than close on a choice that did not resolve --
				// same reasoning as updateBrowsing.
				return nil, -1, false
			}
			mo.images = append(mo.images, opt)
			mo.stopScan()
			mo.finding = false
			return nil, len(mo.images) - 1, true
		}
	}

	var cmd tea.Cmd
	mo.findList, cmd = mo.findList.Update(msg)
	return cmd, -1, false
}

// renderModal composites the open modal over an already-composed screen.
//
// It uses lipgloss v2's compositor rather than splicing lines by hand: the
// layer carries its own x/y/z and the renderer does the ANSI-aware cutting,
// which is the part a hand-rolled overlay gets wrong (a styled line cut by
// byte offset slices an escape sequence in half).
//
// screen is already terminal-sized — Place padded it — so the modal's
// position is computed straight against m.width/m.height.
func (m model) renderModal(screen string) string {
	if m.modal == nil {
		return screen
	}
	box := m.modal.view()
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	// A terminal too narrow or short to center in gets the modal flush to the
	// corner rather than at a negative offset, which would clip its left edge
	// and its border with it.
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

// view renders the modal box. The title carries the drilled-into OS so the
// second level says what it is a list of.
func (mo *imageModal) view() string {
	if mo.browsing {
		return mo.viewBrowsing()
	}
	if mo.finding {
		return mo.viewFinding()
	}
	title := "image"
	hint := "enter choose · esc cancel"
	if mo.level == levelVariant {
		title = "image" + glyphSep + mo.osName
		hint = "enter choose · esc back"
	}
	// The body is held at a fixed width rather than left to hug its content.
	// pane() only ever forces a width DOWN, so without this the box was as
	// wide as whatever happened to be in it — 31 cells at the OS level and 41
	// at the variant level — and drilling in made the border visibly jump.
	body := lipgloss.NewStyle().Width(modalContentWidth).
		Render(mo.list.View() + "\n\n" + dimStyle.Render(hint))
	return pane(title, body, modalContentWidth+paneFrame())
}

// viewFinding renders the fuzzy finder in place of the variant list: the list
// plus one status line that says whether the scan is still running or came
// up empty, so an empty pane doesn't read as "there are no images".
func (mo *imageModal) viewFinding() string {
	mo.ensureFindList()
	var status string
	switch {
	case !mo.scanDone:
		status = dimStyle.Render("searching " + homeDir() + "…")
	case len(mo.findList.Items()) == 0:
		status = dimStyle.Render("no disk images found under " + homeDir())
	}
	hint := dimStyle.Render("enter choose · esc back")
	if status != "" {
		hint = status + "\n" + hint
	}
	body := lipgloss.NewStyle().Width(modalContentWidth).
		Render(mo.findList.View() + "\n\n" + hint)
	return pane("image"+glyphSep+"find", body, modalContentWidth+paneFrame())
}

// viewBrowsing renders the filepicker in place of the variant list.
//
// filepicker.Model has no width of its own — a row is as wide as its
// permissions, size and filename happen to add up to — so unlike every other
// row in this modal, its lines are truncated by hand rather than trusted to
// stay inside modalContentWidth on their own.
func (mo *imageModal) viewBrowsing() string {
	lines := strings.Split(mo.picker.View(), "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, modalContentWidth, "")
	}
	body := lipgloss.NewStyle().Width(modalContentWidth).
		Render(strings.Join(lines, "\n") + "\n\n" + dimStyle.Render("enter select · esc back"))
	return pane("image"+glyphSep+"browse", body, modalContentWidth+paneFrame())
}
