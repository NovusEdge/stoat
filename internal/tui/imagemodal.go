package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/theme"
)

// The image picker has two levels: an OS list, then a variant list within
// it. Alpine ships two variants, standard and virt (66 MiB virtio-only vs
// 352 MiB). A flat row that cycles with left/right cannot show why two
// Alpines exist or which one to pick.
//
// The picker is a modal, not another form row. The variant level needs its
// own cursor. A cycling row can only show one dimension at a time.

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
// the form's image slice. The item carries idx directly instead of it being
// inferred from the cursor. bubbles/list indexes into its filtered view, so
// a cursor-based index would point at the wrong image once a filter applies.
type variantItem struct {
	idx         int
	opt         imageOption
	chooseImage bool // the byo group's trailing leaf: opens the fuzzy path picker instead of selecting opt
}

func (i variantItem) FilterValue() string {
	if i.chooseImage {
		return "choose an image…"
	}
	return i.opt.variantLabel()
}

// foundItem is one row of the fuzzy finder. FilterValue returns the full
// path, not the base name. A query can then narrow by directory ("dl alp")
// as well as by file name. This is what makes a flat list of every image on
// the machine usable.
type foundItem struct{ img foundImage }

func (i foundItem) FilterValue() string { return i.img.path }

// variantLabel distinguishes this image from others sharing its OS. It is
// the catalog's variant label, or the filename for a BYO file. The filename
// is the only way to tell two BYO files apart.
//
// BYO filenames are truncated. They are the one label here with no fixed
// bound. ubuntu-24.04-server-cloudimg-amd64.img is 38 cells wide against a
// 40-cell box. An overlong name would push the modal's border out of square.
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
// definition, which is what makes it BYO, so it reports no download state.
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
		// A group with several variants shows the count: this tells the user
		// a second Alpine exists to choose between. A group with one variant
		// shows its size instead. "1 image" tells the user nothing, and a
		// single-variant OS resolves straight from this level, the only
		// place its size ever shows.
		trailer := fmt.Sprintf("%d images", len(it.idxs))
		switch {
		case len(it.idxs) == 1:
			trailer = it.only.sizeLabel()
		case it.os == byoGroup && len(it.idxs) == 0:
			// No BYO file has been found under isos/ yet, but the group still
			// has to be selectable, since it's the only route to choose an image….
			trailer = "choose an image…"
		}
		label := fmt.Sprintf("%-*s", modalVariantWidth, it.os)
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		fmt.Fprint(w, cursor+label+dimStyle.Render(trailer))
	case variantItem:
		if it.chooseImage {
			// No size or status column: it isn't a file, it's a door to the
			// picker screen, so those columns would just be blank.
			label := fmt.Sprintf("%-*s", modalVariantWidth, "choose an image…")
			if index == m.Index() {
				label = selStyle.Render(label)
			}
			fmt.Fprint(w, cursor+label+dimStyle.Render("search or paste"))
			return
		}
		// A styled substring ends in \x1b[0m, which also resets the enclosing
		// style. The status column stays outside the selection wrap for this
		// reason; a highlighted row would otherwise render everything after
		// it as unhighlighted. Padding happens on plain text, then styling
		// applies, for the same reason.
		label := fmt.Sprintf("%-*s", modalVariantWidth, it.opt.variantLabel())
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		// Size right-aligned so the digits line up column-wise; padded plain,
		// then dimmed as a whole segment.
		size := dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, it.opt.sizeLabel()))
		fmt.Fprint(w, cursor+label+size+"  "+it.opt.statusLabel())
	case foundItem:
		fmt.Fprint(w, cursor+foundRow(it.img, m.Width()-lipgloss.Width(cursor), index == m.Index()))
	}
}

// modalContentWidth is the box's floor width. modalRows is its floor row
// count. Every list starts at these values until resize grows them. Both
// fit the smallest terminal stoat renders panes in, 60x20 (smallWidth x
// smallHeight). At that size the box is modalContentWidth+paneFrame() wide
// and modalRows+6 tall.
const (
	modalContentWidth = 46
	modalRows         = 6
)

// modalMaxContentWidth and modalMaxRows are a backstop, not a design target.
// The box is meant to fill the terminal, not stop at a conservative width.
// These values never bind on real hardware. They exist only to stop a
// pathological terminal size from producing an absurd box.
const (
	modalMaxContentWidth = 1000
	modalMaxRows         = 500
)

// modalWidthFraction and modalHeightFraction set how much of the terminal
// the box can claim on resize, before its own frame is subtracted. A
// fraction, not a fixed margin: a fixed margin leaves a couple of cells on
// a small terminal, but almost nothing on a large one (200 columns minus 4
// cells is still 190, which fills the screen instead of floating over it).
// A fraction keeps the margin proportionate at any terminal size.
//
// 0.85/0.8, not looser: high enough that the box keeps growing
// aggressively, which is the point of this feature. Low enough that a
// large terminal keeps a visible margin on every side, centred and
// floating. A full-terminal takeover was already tried and rejected in
// this project's layout work.
const (
	modalWidthFraction  = 0.85
	modalHeightFraction = 0.8
)

// modalHeightChrome is the vertical space the box spends on everything
// that is not a list row: the pane border, padding, title, and, on the
// byo screen, the text field and a two-line hint or error. resize
// subtracts this from the terminal's allotted share before turning the
// remainder into rows. Growing rows can then never overflow the box's
// fixed chrome. The value is sized to the byo screen's tallest state, an
// inline error that wraps to two lines, the worst case the shared row
// count must fit.
const modalHeightChrome = 11

// modalSizeWidth is the size column, right-aligned so digits line up down
// the list. Comparing sizes is the point of showing them, and a ragged left
// edge makes that harder.
//
// The width is nine, not eight. humanBytes keeps one decimal below 100
// ("66.0 MiB"), so the widest value is "~66.0 MiB", not the "~595 MiB" the
// biggest image would suggest. At eight cells, the status column shifted
// by one cell on rows with a small image, exactly alpine-virt, the row
// this feature exists to make comparable.
const modalSizeWidth = 9

// foundRow lays out one byo result row: the base file name, the directory,
// and the size, in that priority order. The name never truncates. This was
// the reason the byo screen exists: "we need full file names for it to be
// useful". Unlike every other row in this modal, these columns are not
// fixed widths that sum to the list width. Layout works backward from the
// name. What is left goes to the directory, elided from the left so the
// deepest part stays, since deep directories are what tell two same-named
// files apart. The size is the first column dropped when space runs out.
//
// The size column can drift out of alignment row to row when names vary in
// length and space is tight. This is accepted. The alternative is
// truncating the name to keep a column aligned, and the name must not give.
func foundRow(img foundImage, width int, selected bool) string {
	name := filepath.Base(img.path)

	if lipgloss.Width(name) >= width {
		// The name loses characters only here: it alone already reaches or
		// exceeds the row's full width, so no other space is left to take.
		// It is elided from the middle, not the tail. The version and
		// extension near the end identify the file, so the head gives way
		// first.
		name = middleElide(name, width)
	}
	label := name
	if selected {
		label = selStyle.Render(label)
	}

	rest := width - lipgloss.Width(name)
	if rest <= 0 {
		return label
	}
	rest -= foundGap
	if rest <= 0 {
		// Room for the name and nothing else, not even the gap after it.
		return label + strings.Repeat(" ", width-lipgloss.Width(name))
	}
	gap := strings.Repeat(" ", foundGap)

	switch {
	case rest >= modalSizeWidth+foundMinDirWidth:
		// Room for both: the directory takes everything left after the size
		// column is reserved.
		dirWidth := rest - modalSizeWidth
		dir := dimStyle.Render(fmt.Sprintf("%-*s", dirWidth, dirTail(filepath.Dir(img.path), dirWidth)))
		size := dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, humanBytes(img.size)))
		return label + gap + dir + size
	case rest >= modalSizeWidth:
		// No room for a directory at all; keep the size, since it costs
		// less width and still tells the images apart by weight.
		return label + gap + strings.Repeat(" ", rest-modalSizeWidth) + dimStyle.Render(fmt.Sprintf("%*s", modalSizeWidth, humanBytes(img.size)))
	default:
		// Not even the size fits next to a name this long; pad and stop.
		return label + gap + strings.Repeat(" ", rest)
	}
}

// foundGap is the blank run between the name and whatever follows it.
// Without it, a directory or size butts straight up against the name the
// moment the name isn't padded to a fixed column anymore.
const foundGap = 2

// foundMinDirWidth is the least a directory column is worth showing at.
// Below it, "…/x" says almost nothing, so the row drops the directory
// entirely rather than draw a sliver of one.
const foundMinDirWidth = 6

// dirTail elides dir to width cells, keeping the RIGHTMOST part. The deepest
// directories are what actually distinguishes two files sharing a name, so
// that's what stays; the head is cut and marked with a leading "…".
func dirTail(dir string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(dir)
	if w <= width {
		return dir
	}
	return ansi.TruncateLeft(dir, w-(width-1), "…")
}

// middleElide shortens s to width cells by cutting from the middle and
// marking the cut with "…", e.g. "alpine-stand…x86_64.iso". dirTail only
// needs to keep one end. A file name's identifying parts sit at both ends:
// the base name up front, the version and extension at the back. The tail
// keeps the larger half, since the extension is what a user scans for
// first when several files share a prefix.
//
// middleElide runs only when nothing else can be dropped to fit the name.
// Every other case in foundRow keeps the name whole.
func middleElide(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		if width == 1 {
			return "…"
		}
		return ""
	}
	keep := width - 1 // one cell for the "…"
	tail := (keep + 1) / 2
	head := keep - tail
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// imageModal is the two-level picker. One list, re-populated on drill-down,
// rather than two lists: two would mean two cursors to keep consistent and
// two things to size against the terminal.
type imageModal struct {
	list   list.Model
	level  modalLevel
	groups []osItem
	images []imageOption
	osName string // the group drilled into; meaningful at levelVariant

	// contentWidth and rows are the box's content width and row count for
	// the current render. resize recomputes both every frame from the
	// terminal size. Both start at their floor constants. A modal built
	// directly, as most tests do, and never passed through renderModal,
	// draws at this fixed floor size and never resizes.
	contentWidth int
	rows         int

	// byo backs the byo group's one leaf: a focused text field over a
	// fuzzy-filtered list of every disk image under $HOME. This single
	// screen replaced the separate path field, finder, and tree browser. It
	// is a sub-mode because it owns the keyboard while open. Arrow keys
	// here move the selection, unlike at the OS or variant levels.
	byo      bool
	byoInput textinput.Model
	// byoList is the fuzzy-filtered result list. Its own filter prompt is
	// hidden (SetShowFilter(false)); byoInput is the one visible text
	// field, and its value drives byoList.SetFilterText on every keystroke.
	byoList      list.Model
	byoListReady bool   // whether byoList has been built by ensureByoList
	byoErr       string // set when the last enter's typed path failed to resolve; cleared on the next edit

	// scanCh is the channel the running scan streams batches down. Stored so
	// updateByo can re-issue waitForImages after each batch; openByo's local
	// ch goes out of scope the moment it returns.
	scanCh <-chan []foundImage
	// scanCancel, closed by stopScan, tells the scan goroutine to give up
	// rather than block forever on a send nobody will read (see scanImages).
	scanCancel chan struct{}
	// scanGen is the current scan's generation, stamped into every
	// imagesFoundMsg openByo issues. updateByo drops any message whose
	// generation doesn't match; see imagesFoundMsg's doc.
	scanGen int
	// scanDone distinguishes "found nothing yet" from "found nothing": an
	// empty pane means opposite things before and after the walk ends.
	scanDone bool
}

// newImageList builds the modal's list component. Filtering is off. The
// catalog holds five OSes, so a search box earns nothing here. Filtering
// would also make esc ambiguous: esc must mean "go back a level" in this
// modal, but bubbles/list claims esc for clearing a filter.
func newImageList(width, rows int) list.Model {
	l := list.New(nil, imageDelegate{}, width, rows)
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

// groupImages buckets the form's images by OS, preserving arrival order.
// That order is the catalog's own: hand-written with the most generally
// useful image first. Sorting would replace that choice with alphabetical
// noise. BYO files land in one trailing group.
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
	// size, since it is the only level a single-variant OS is ever seen at.
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
	mo := &imageModal{
		contentWidth: modalContentWidth,
		rows:         modalRows,
		images:       images,
		groups:       groupImages(images),
	}
	mo.list = newImageList(mo.contentWidth, mo.rows)
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
		// choose an image… trails the list. Existing BYO files are the
		// images the user most likely wants. The door to typing or
		// searching for a different one is the fallback at the bottom, not
		// the first thing the cursor lands on.
		items = append(items, variantItem{chooseImage: true})
	}
	mo.level = levelVariant
	mo.osName = g.os
	mo.list.SetItems(items)
	mo.list.Select(0)
	mo.syncHeight(len(items))
}

// syncHeight shrinks the list to the rows it actually has, so a two-item
// level does not draw a box with four blank lines in it. bubbles/list pads
// its viewport to whatever height it is given.
func (mo *imageModal) syncHeight(n int) {
	if n > mo.rows {
		n = mo.rows
	}
	if n < 1 {
		n = 1
	}
	mo.list.SetShowPagination(len(mo.list.Items()) > mo.rows)
	mo.list.SetHeight(n)
}

// update handles one message while the modal is open. It returns the index
// of the chosen image (-1 when nothing was chosen yet) and whether the
// modal should close.
//
// ctrl+c is absent here. app.go handles it centrally before the message
// switch. Duplicating it per sub-mode caused a past regression.
func (mo *imageModal) update(msg tea.Msg) (tea.Cmd, int, bool) {
	if mo.byo {
		return mo.updateByo(msg)
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
			// A group with one image has nothing to choose between.
			// Drilling in to press enter again would be a keystroke for
			// no decision. byo is exempt even at exactly one image. It
			// always drills, because that one image sits alongside
			// choose an image…, which the shortcut would otherwise make
			// unreachable.
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
			if it.chooseImage {
				return mo.openByo(), -1, false
			}
			return nil, it.idx, true
		}
	}

	var cmd tea.Cmd
	mo.list, cmd = mo.list.Update(msg)
	return cmd, -1, false
}

// expandHome resolves a leading ~ or ~/... to the user's home directory. Only
// that form: ~user/... names someone else's home, which this never had a way
// to look up, so it is left untouched and will simply fail to stat like any
// other bad path.
func expandHome(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}

// openByo swaps the variant list for the byo screen: a focused text field
// over a fuzzy-filtered list of every disk image under $HOME. theme.TextInput
// is the same constructor form.go and edit.go use. This makes the field
// match every other field in the app, instead of bubbles' stock styling.
//
// The returned cmd batches the input's cursor blink with the scan's first
// pump. Each imagesFoundMsg re-issues the pump until the channel closes.
// Without this the scan runs and nothing reads the results.
func (mo *imageModal) openByo() tea.Cmd {
	mo.ensureByoList()
	// Cleared, not appended to: re-entering the screen starts a fresh scan,
	// and the old scan's results are still sitting in byoList's items from
	// last time. Without this every re-entry doubles the whole list.
	mo.byoList.SetItems(nil)
	mo.stopScan() // in case the previous scan is somehow still running

	ti := theme.TextInput()
	ti.Placeholder = "type to search, or paste a path"
	ti.SetWidth(mo.contentWidth)
	mo.byoInput = ti
	mo.byo = true
	mo.byoErr = ""
	mo.scanDone = false
	mo.scanGen++

	cancel := make(chan struct{})
	mo.scanCancel = cancel
	ch := scanImages(homeDir(), cancel)
	mo.scanCh = ch
	return tea.Batch(mo.byoInput.Focus(), waitForImages(ch, mo.scanGen))
}

// stopScan tells the running scan's goroutine to give up, instead of
// blocking forever on a batch nobody will read. It also forgets the
// channel, so a stray repump cannot target it. stopScan runs whenever the
// byo screen is left: esc, choosing an image, or the modal closing outright.
func (mo *imageModal) stopScan() {
	if mo.scanCancel != nil {
		close(mo.scanCancel)
		mo.scanCancel = nil
	}
	mo.scanCh = nil
}

// ensureByoList lazily builds the byo screen's result list on first use. It
// is separate from openByo so setFound and view work correctly when a
// caller sets mo.byo directly instead of going through the scan, as the
// tests do. No live scan is required to see the screen.
//
// The list's own filter prompt stays hidden (SetShowFilter(false)).
// byoInput is the one visible text field. updateByo drives the list's
// filter from its value via SetFilterText, instead of letting the list
// manage its own filter.
func (mo *imageModal) ensureByoList() {
	if mo.byoListReady {
		return
	}
	l := list.New(nil, imageDelegate{}, mo.contentWidth, mo.rows)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(true)
	l.Styles = listStyles(l.Styles)
	mo.byoList = l
	mo.byoListReady = true
}

// setFound appends a batch and re-sorts. Sorting on every batch, not once
// at the end, keeps the visible order stable while results stream in. Rows
// that jump under a cursor the user is already moving are worse than a
// slightly late sort.
func (mo *imageModal) setFound(found []foundImage) {
	mo.ensureByoList()
	items := mo.byoList.Items()
	for _, f := range found {
		items = append(items, foundItem{img: f})
	}
	sort.SliceStable(items, func(a, b int) bool {
		return items[a].(foundItem).img.path < items[b].(foundItem).img.path
	})
	mo.byoList.SetItems(items)
	mo.byoList.SetShowPagination(len(items) > mo.rows)
	// A live filter must keep matching the grown item set, or a batch that
	// lands after the user has already typed a query would silently show
	// unfiltered results until the next keystroke.
	mo.byoList.SetFilterText(mo.filterQuery())
}

// filterQuery is what byoInput's value actually filters against. It expands
// the same way a typed path expands on enter. Scanned paths are absolute,
// so a bare "~" or "~/..." matches nothing in them character-for-character.
// Fuzzy matching still requires every rune to appear in order. Without
// expansion, the most natural way to type a home-relative path, such as
// ~/Downloads, made the screen look like it found nothing.
func (mo *imageModal) filterQuery() string {
	return expandHome(mo.byoInput.Value())
}

// updateByo owns the keyboard while the byo screen is open. Up/down, and
// ctrl+p/ctrl+n, move byoList's cursor directly instead of going through
// its Update. The field above it must stay focused for typing to keep
// filtering. Routing arrow keys through the list's own Update risks its
// filter-input handling stealing them. Every other key press goes to
// byoInput, and its value re-drives byoList's filter live.
func (mo *imageModal) updateByo(msg tea.Msg) (tea.Cmd, int, bool) {
	mo.ensureByoList()
	if found, ok := msg.(imagesFoundMsg); ok {
		if found.gen != mo.scanGen {
			// This message came from an abandoned scan (esc, or re-entering
			// the byo screen since it was issued). It is dropped outright.
			// Repumping it would read the wrong channel, and a stale done
			// would mark the new scan finished while it is still running.
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
			// Back to the variant list, not out of the modal: esc means
			// "back a level" everywhere else here, and closing outright on
			// it would throw away the group the user already drilled into.
			mo.stopScan()
			mo.byo = false
			return nil, -1, false
		case "up", "ctrl+p":
			mo.byoList.CursorUp()
			return nil, -1, false
		case "down", "ctrl+n":
			mo.byoList.CursorDown()
			return nil, -1, false
		case "enter":
			if it, ok := mo.byoList.SelectedItem().(foundItem); ok {
				opt, err := byoOptionFromPath(it.img.path)
				if err == nil {
					mo.images = append(mo.images, opt)
					mo.stopScan()
					mo.byo = false
					return nil, len(mo.images) - 1, true
				}
				// The file was deleted or unmounted since the scan listed
				// it, or the list is simply empty with no filter match.
				// Either way, fall through to resolving the typed text as
				// a path directly. This is how an image outside $HOME gets
				// chosen.
			}
			path := strings.TrimSpace(mo.byoInput.Value())
			if path == "" {
				// Nothing typed and nothing selected; enter does nothing
				// rather than trying to resolve "" as a path.
				return nil, -1, false
			}
			opt, err := byoOptionFromPath(expandHome(path))
			if err != nil {
				// Stay open and say why: closing or going silent on a bad
				// path would leave the user guessing what happened.
				mo.byoErr = err.Error()
				return nil, -1, false
			}
			mo.images = append(mo.images, opt)
			mo.stopScan()
			mo.byo = false
			return nil, len(mo.images) - 1, true
		}
	}

	var cmd tea.Cmd
	mo.byoInput, cmd = mo.byoInput.Update(msg)
	mo.byoErr = ""
	mo.byoList.SetFilterText(mo.filterQuery())
	return cmd, -1, false
}

// renderModal composites the open modal over an already-composed screen.
//
// It uses lipgloss v2's compositor, not hand-spliced lines. The layer
// carries its own x/y/z, and the renderer does the ANSI-aware cutting. A
// hand-rolled overlay gets this wrong: cutting a styled line by byte offset
// can slice an escape sequence in half.
//
// screen is already terminal-sized; Place padded it. The modal's position
// is computed directly against m.width and m.height.
func (m model) renderModal(screen string) string {
	if m.modal == nil {
		return screen
	}
	// This is the one place a modal learns the terminal's size. renderModal
	// runs every frame with m.width and m.height already known. Resizing
	// here keeps the box in step, without the modal reaching for a global
	// or every caller threading size through by hand. A modal built
	// directly and viewed without going through here, as most tests do,
	// never resizes and draws at its original fixed size.
	m.modal.resize(m.width, m.height)
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
	if mo.byo {
		return mo.viewByo()
	}
	title := "image"
	hint := "enter choose · esc cancel"
	if mo.level == levelVariant {
		title = "image" + glyphSep + mo.osName
		hint = "enter choose · esc back"
	}
	// The body is held at a fixed width, not left to hug its content.
	// pane() only ever forces a width down. Without this the box was as
	// wide as whatever it held, 31 cells at the OS level and 41 at the
	// variant level, so drilling in made the border visibly jump.
	body := lipgloss.NewStyle().Width(mo.contentWidth).
		Render(mo.list.View() + "\n\n" + dimStyle.Render(hint))
	return pane(title, body, mo.contentWidth+paneFrame())
}

// viewByo renders the text field and its fuzzy-filtered result list in
// place of the variant list: the field, the list, then one status line.
// The status line says whether the scan is still running or came up
// empty, so an empty pane does not read as "there are no images". A
// typed-path error takes the status line's place instead of sitting
// beside it, so the user sees why the last enter failed instead of a
// stale "searching".
func (mo *imageModal) viewByo() string {
	mo.ensureByoList()
	var status string
	switch {
	case !mo.scanDone:
		status = dimStyle.Render("searching " + homeDir() + "…")
	case len(mo.byoList.Items()) == 0:
		status = dimStyle.Render("no disk images found under " + homeDir())
	}
	hint := dimStyle.Render("enter select · esc back")
	switch {
	case mo.byoErr != "":
		hint = errStyle.Render(mo.byoErr) + "\n" + hint
	case status != "":
		hint = status + "\n" + hint
	}
	body := lipgloss.NewStyle().Width(mo.contentWidth).
		Render(mo.byoInput.View() + "\n" + mo.byoList.View() + "\n\n" + hint)
	return pane("image"+glyphSep+"byo", body, mo.contentWidth+paneFrame())
}

// resize recomputes the box's content width and row count from the
// terminal's current size. Both are clamped between the fixed floors
// above, the original pre-growth size, and modalMaxContentWidth /
// modalMaxRows. resize then pushes the new size into whichever lists and
// input already exist.
//
// resize is idempotent and cheap enough to call every frame. SetWidth and
// SetHeight are no-ops on an unchanged value, so no guard is needed against
// calling this when nothing changed.
func (mo *imageModal) resize(termWidth, termHeight int) {
	widthBudget := int(float64(termWidth)*modalWidthFraction) - paneFrame()
	mo.contentWidth = max(modalContentWidth, min(modalMaxContentWidth, widthBudget))
	heightBudget := int(float64(termHeight)*modalHeightFraction) - modalHeightChrome
	mo.rows = max(modalRows, min(modalMaxRows, heightBudget))

	mo.list.SetWidth(mo.contentWidth)
	mo.syncHeight(len(mo.list.Items()))
	if mo.byoListReady {
		mo.byoList.SetWidth(mo.contentWidth)
		mo.byoList.SetHeight(mo.rows)
		mo.byoList.SetShowPagination(len(mo.byoList.Items()) > mo.rows)
	}
	if mo.byo {
		mo.byoInput.SetWidth(mo.contentWidth)
	}
}
