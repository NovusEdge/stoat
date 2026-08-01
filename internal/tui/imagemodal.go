package tui

import (
	"fmt"
	"io"

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
	idx int
	opt imageOption
}

func (i variantItem) FilterValue() string { return i.opt.variantLabel() }

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
		if len(it.idxs) == 1 {
			trailer = it.only.sizeLabel()
		}
		label := fmt.Sprintf("%-*s", modalVariantWidth, it.os)
		if index == m.Index() {
			label = selStyle.Render(label)
		}
		fmt.Fprint(w, cursor+label+dimStyle.Render(trailer))
	case variantItem:
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

// imageModal is the two-level picker. One list, re-populated on drill-down,
// rather than two lists: two would mean two cursors to keep consistent and
// two things to size against the terminal.
type imageModal struct {
	list   list.Model
	level  modalLevel
	groups []osItem
	images []imageOption
	osName string // the group drilled into; meaningful at levelVariant
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
	l.Styles.NoItems = dimStyle
	l.Styles.PaginationStyle = lipgloss.NewStyle()
	l.Styles.ActivePaginationDot = accentStyle
	l.Styles.InactivePaginationDot = dimStyle
	return l
}

// groupImages buckets the form's images by OS, preserving the order they
// arrive in — that order is the catalog's own, which is hand-written with the
// most generally useful image first, and sorting would replace that judgement
// with alphabetical noise. BYO files land in one trailing group.
func groupImages(images []imageOption) []osItem {
	var groups []osItem
	at := map[string]int{}
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
		j, seen := at[name]
		if !seen {
			at[name] = len(groups)
			groups = append(groups, osItem{os: name})
			j = len(groups) - 1
		}
		groups[j].idxs = append(groups[j].idxs, i)
	}
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
	items := make([]list.Item, 0, len(g.idxs))
	for _, idx := range g.idxs {
		items = append(items, variantItem{idx: idx, opt: mo.images[idx]})
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
			if len(g.idxs) == 1 {
				return nil, g.idxs[0], true
			}
			mo.drill(g)
			return nil, -1, false
		default:
			it, ok := mo.list.SelectedItem().(variantItem)
			if !ok {
				return nil, -1, false
			}
			return nil, it.idx, true
		}
	}

	var cmd tea.Cmd
	mo.list, cmd = mo.list.Update(msg)
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
