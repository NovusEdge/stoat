package tui

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// vmItem is one row of the VM list. A single type covers both good VMs and
// broken ones (a directory whose vm.toml won't parse) because the cursor
// ranges over them as one sequence — modelling them as two lists would mean
// reimplementing that interleaving on top of bubbles/list.
type vmItem struct {
	vm     *config.VM     // nil for a broken entry
	broken *config.Broken // nil for a good VM
}

func (i vmItem) name() string {
	if i.vm != nil {
		return i.vm.Name
	}
	return i.broken.Name
}

// FilterValue is what "/" searches. Name only: mode and state are volatile
// (a VM stops, and a filter that matched it stops matching), and searching
// for "live" to find a VM you named "liveserver" is the common case.
func (i vmItem) FilterValue() string { return i.name() }

// vmDelegate renders stoat's own row format inside bubbles/list, so adopting
// the component's scrolling and filtering doesn't mean adopting its visual
// language — no purple selection bar, no per-item description block.
type vmDelegate struct{}

func (vmDelegate) Height() int  { return 1 }
func (vmDelegate) Spacing() int { return 1 } // the blank line between rows

func (vmDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d vmDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(vmItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	cursor := "  "
	if selected {
		cursor = selStyle.Render(glyphCursor)
	}

	if it.broken != nil {
		// A broken vm.toml is an error, not an idle state: the glyph is red
		// even when the row isn't selected, while the text stays muted so a
		// whole line of red isn't shouting from the list.
		plain := fmt.Sprintf("%-14s broken: %s", it.broken.Name, brokenReason(it.broken.Err))
		if selected {
			plain = selStyle.Render(plain)
		} else {
			plain = downStyle.Render(plain)
		}
		fmt.Fprint(w, cursor+errStyle.Render(glyphBroken)+" "+plain)
		return
	}

	v := it.vm
	dot, dotStyle := glyphStopped, downStyle
	state := dimStyle.Render("—")
	if qemu.Running(v) {
		dot, dotStyle = glyphRunning, upStyle
		state = fmt.Sprintf("up %s  :%d", qemu.Uptime(v), v.SSHPort)
	}
	// The dot and the state stay OUTSIDE the selection wrap: a styled
	// substring ends in \x1b[0m, which resets the enclosing style too, so
	// wrapping a row that starts with a coloured dot leaves everything after
	// it unhighlighted, and a trailing dim "—" would render unbolded inside
	// an otherwise highlighted row.
	label := fmt.Sprintf("%-14s %-5s %5dM %2dc  ", v.Name, v.Mode, v.RAM, v.CPUs)
	if selected {
		label = selStyle.Render(label)
	}
	fmt.Fprint(w, cursor+dotStyle.Render(dot)+" "+label+state)
}

// listWidth and listVisibleRows size the VM list. Fixed rather than derived
// from the terminal so the pane doesn't resize as VMs come and go; the row
// budget is what bubbles/list paginates against.
const (
	// Wide enough for the widest row the format can produce with values that
	// are actually reachable: a 14-char name, 5-digit RAM, 2-digit cpus, an
	// uptime just under 1000 hours and a 5-digit port (the edit form allows
	// up to 65535). Sized off the RUNNING row on purpose — a stopped one is
	// only 38 cells, which is why an undersized value looks fine in every
	// test render and then wraps the port onto its own line the moment
	// something is actually up. A terminal narrower than this still clamps
	// (paneAt bounds to the window), which is unavoidable at that size.
	listWidth        = 60
	listVisibleRows  = 6
	listMinRows      = 2
	listRowsHeadroom = 14 // banner, pane frame, status, footer
)

// newVMList builds the list component with stoat's styling: the component's
// own title, status bar and help are all off, because this program already
// draws a pane title and a footer and would otherwise show two of each.
func newVMList() list.Model {
	l := list.New(nil, vmDelegate{}, listWidth, listVisibleRows*2)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	// The component draws its own filter prompt at the top of its viewport;
	// stoat renders the search line outside the pane instead, so the pane
	// stays nothing but rows and doesn't change width when search opens.
	l.SetShowFilter(false)
	l.SetFilteringEnabled(true)
	l.SetStatusBarItemName("vm", "vms")

	l.Styles.NoItems = dimStyle
	l.Styles.Filter.Focused.Prompt = accentStyle
	l.Styles.Filter.Blurred.Prompt = accentStyle
	l.Styles.Filter.Cursor.Color = th.accent
	l.Styles.PaginationStyle = lipgloss.NewStyle()
	l.Styles.ActivePaginationDot = accentStyle
	l.Styles.InactivePaginationDot = dimStyle

	fs := l.FilterInput.Styles()
	fs.Focused.Prompt = accentStyle
	fs.Blurred.Prompt = accentStyle
	fs.Cursor.Color = th.accent
	l.FilterInput.SetStyles(fs)
	l.FilterInput.Prompt = "search "
	l.FilterInput.Placeholder = "vm name"
	return l
}

// vmItems flattens the model's VMs and broken directories into list items,
// good ones first, matching the order the cursor used to walk.
func vmItems(vms []*config.VM, broken []config.Broken) []list.Item {
	items := make([]list.Item, 0, len(vms)+len(broken))
	for _, v := range vms {
		items = append(items, vmItem{vm: v})
	}
	for i := range broken {
		items = append(items, vmItem{broken: &broken[i]})
	}
	return items
}

// selectedItem returns the row under the cursor, honouring an active filter
// (bubbles/list indexes into the filtered view, not the full slice).
func (m model) selectedItem() (vmItem, bool) {
	it, ok := m.list.SelectedItem().(vmItem)
	return it, ok
}

// listHeight sizes the list to whichever is smaller: the rows that fit the
// terminal, or the rows there actually are. bubbles/list pads its viewport to
// whatever height it is given, so sizing it to the terminal alone left a tall
// column of blank lines under three VMs.
func listHeight(items, termHeight int, paginated bool) int {
	n := listRows(termHeight)
	if items < n {
		n = items
	}
	if n < 1 {
		n = 1
	}
	// Each row costs a line plus the delegate's spacer. The component
	// computes items-per-page as height/(itemHeight+spacing), so shaving the
	// last row's spacer here would round the division down and strand a VM on
	// page two; viewList trims the resulting trailing blank instead.
	h := n * 2
	if paginated {
		// The pagination line is drawn inside the height the component is
		// given, so it needs room or the last VM is pushed to page two. It
		// costs exactly one line: bubbles only adds a top margin to it when
		// the delegate's spacing is 0, and ours is 1. Budgeting two made the
		// pane a line taller than the terminal, which flipped View() to
		// top-align and cut the footer off the bottom.
		h++
	}
	return h
}

// listRows is how many rows fit the terminal, so a tall window shows more
// VMs instead of paginating early. Clamped at the bottom so a short terminal
// still shows something rather than an empty pane.
func listRows(height int) int {
	if height <= 0 {
		return listVisibleRows
	}
	// Each row costs its own line plus the delegate's blank spacer.
	n := (height - listRowsHeadroom) / 2
	if n < listMinRows {
		return listMinRows
	}
	if n > 12 {
		return 12
	}
	return n
}

// syncListHeight resizes the list to the rows actually visible right now, so
// the pane shrinks with an applied filter instead of leaving a column of
// blank lines where the filtered-out VMs used to be. Called after every
// list update, since filtering resolves asynchronously through a Cmd and the
// visible count is only correct once that lands.
func (m *model) syncListHeight() {
	n := len(m.list.VisibleItems())
	if n == 0 && len(m.list.Items()) > 0 {
		n = 1 // room for the "no vm matches" line
	}
	// Pagination dots only earn their two lines when there is more than one
	// page; otherwise they leave dead space at the bottom of the pane.
	paginated := n > listRows(m.height)
	m.list.SetShowPagination(paginated)
	m.list.SetHeight(listHeight(n, m.height, paginated))
}

// filterActive reports whether the filter input owns the keyboard. While it
// does, stoat's own single-letter bindings (n, d, p, s, q) must not fire —
// they are characters the user is typing into the search box.
func (m model) filterActive() bool { return m.list.SettingFilter() }

// listStatusLine renders the filter state under the pane, so an applied
// filter is visible after the input closes — otherwise a filtered list looks
// like VMs have gone missing.
func listStatusLine(l list.Model) string {
	if l.SettingFilter() {
		return l.FilterInput.View()
	}
	if l.IsFiltered() {
		return dimStyle.Render(fmt.Sprintf("search %q — %d of %d · esc clears",
			l.FilterValue(), len(l.VisibleItems()), len(l.Items())))
	}
	return ""
}
