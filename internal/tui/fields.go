package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Every screen that shows rows of "label   value" renders through fields.
// Before this, five files each hand-padded their own rows with
// fmt.Sprintf("%-8s") — at three different label widths — and three of them
// repeated a bare 11 (as strings.Repeat(" ", 11), as an eleven-space string
// literal, and as contentWidth-11) for the indent a continuation line needs.
// Changing the label column meant finding all of those; missing one left a
// screen misaligned in a way no test covered.
const (
	fieldMarkerWidth = 2  // the focus cursor: "❯ ", or two spaces
	fieldLabelWidth  = 10 // the widest label ("installed") plus a gap
)

// fieldValueColumn is the cell a row's value starts at, for the screens that
// still have to size something against it by hand.
const fieldValueColumn = fieldMarkerWidth + fieldLabelWidth

// fields accumulates the rows of one screen's field block.
//
// width is the block's total content width. Set it and a value too long for
// the row wraps inside the value column, so the continuation lines stay
// under the value rather than running back to the pane's left edge — which
// is what happens if the pane is left to do the wrapping. Zero means "size
// to the content", for blocks short enough that it cannot come up.
type fields struct {
	rows  [][]string
	width int
}

// row adds "label   value". marker carries the focus cursor on the screens
// that have one (the form, the edit form) and is "" on the read-only ones.
// Labels are styled by fields itself; a caller only styles the value.
func (f *fields) row(marker, label, value string) {
	f.rows = append(f.rows, []string{marker, label, value})
}

// hint adds a dim line under the row above, aligned to the value column —
// the sentence saying what that row actually does.
func (f *fields) hint(text string) {
	f.rows = append(f.rows, []string{"", "", dimStyle.Render(text)})
}

// gap separates groups of related rows. Rows are single-spaced and the blank
// line goes between groups, not between every row: the form is the tallest
// screen, and spacing every row overflowed a 24-line terminal.
func (f *fields) gap() { f.rows = append(f.rows, []string{"", "", ""}) }

func (f *fields) String() string {
	if len(f.rows) == 0 {
		return ""
	}
	return table.New().
		// An all-empty border: the table is a layout grid here, not a drawn
		// box — the pane around it owns the only border on screen.
		Border(lipgloss.Border{}).
		BorderTop(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderRow(false).BorderColumn(false).
		// BorderBottom stays ON deliberately. lipgloss v1.1.0's table drops
		// the last data row entirely when it is off (three rows render as
		// two). The border glyphs are all empty, so leaving it on draws
		// nothing and costs no line — turning it off costs a row.
		BorderBottom(true).
		StyleFunc(func(_, col int) lipgloss.Style {
			switch col {
			case 0:
				return lipgloss.NewStyle().Width(fieldMarkerWidth)
			case 1:
				// Dim every label, on every screen. The value is what the
				// user is reading; the label is just what to call it.
				return dimStyle.Width(fieldLabelWidth)
			}
			if f.width > 0 {
				return lipgloss.NewStyle().Width(f.width - fieldValueColumn)
			}
			return lipgloss.NewStyle()
		}).
		Rows(f.rows...).
		String()
}
