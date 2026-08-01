package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
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

// noteMark tags a row as a note rather than a field. It sits in the marker
// cell, which a note never uses, so it cannot collide with a focus cursor.
const noteMark = "\x00note"

// note adds a dim remark under the row above, starting at the LABEL column
// rather than the value column — it is a remark ABOUT the field, not another
// value for it, so it reads wrong indented under the value.
//
// Notes are drawn as their own full-width lines rather than table rows: a
// lipgloss table cannot span columns, and the text is wider than the label
// cell, so in a cell it would wrap mid-word. Splitting the table around them
// is safe precisely because every column here has a FIXED width — the
// segments either side of a note still line up. Callers that leave width at
// zero (the value column then auto-sizes) must not use notes; only the two
// forms do, and both set a width.
func (f *fields) note(text string) {
	f.rows = append(f.rows, []string{noteMark, text, ""})
}

// String renders the block: table segments for runs of field rows, with each
// note on its own line between them.
func (f *fields) String() string {
	if len(f.rows) == 0 {
		return ""
	}
	var out []string
	var run [][]string
	flush := func() {
		if len(run) > 0 {
			out = append(out, f.renderRows(run))
			run = nil
		}
	}
	for _, r := range f.rows {
		if r[0] == noteMark {
			flush()
			// Indented to the label column, so it lines up with the name of
			// the field it is talking about.
			out = append(out, strings.Repeat(" ", fieldMarkerWidth)+dimStyle.Render(r[1]))
			continue
		}
		run = append(run, r)
	}
	flush()
	return strings.Join(out, "\n")
}

func (f *fields) renderRows(rows [][]string) string {
	return table.New().
		// An all-empty border: the table is a layout grid here, not a drawn
		// box — the pane around it owns the only border on screen.
		Border(lipgloss.Border{}).
		BorderTop(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderRow(false).BorderColumn(false).
		// BorderBottom stays ON deliberately: with it off, lipgloss v1.1.0's
		// table drops the last data row entirely (three rows render as two).
		// The glyphs are all empty, so leaving it on draws nothing and costs
		// no line — turning it off costs a row.
		//
		// The real fault is table.computeHeight(), which returns one line too
		// few for a table with no headers, and which String() applies as a
		// hard MaxHeight clamp. That arithmetic is unchanged in lipgloss
		// v2.0.5; v2 only stops CALLING it, by clamping to
		// min(t.height, computeHeight()) — and t.height is 0 unless someone
		// calls .Height(). So the migration must not "clean up" this line on
		// the grounds that v2 fixed the bug: it did not, and calling .Height()
		// on this table brings it straight back, in either version.
		// TestFieldsKeepsEveryRow is the guard.
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
		Rows(rows...).
		String()
}
