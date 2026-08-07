package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// Every screen that shows rows of "label   value" renders through fields.
// Five files used to hand-pad their own rows with fmt.Sprintf("%-8s"), at
// three different label widths. Three of them repeated a bare 11, as
// strings.Repeat(" ", 11), as an eleven-space literal, and as
// contentWidth-11, for the continuation-line indent. Changing the label
// column meant finding every one of those; a missed spot left a screen
// misaligned in a way no test covered.
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
// under the value rather than running back to the pane's left edge, which
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

// hint adds a dim line under the row above, aligned to the value column:
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

// note adds a dim remark under the row above, starting at the label column
// rather than the value column. It is a remark about the field, not
// another value for it, so it reads wrong indented under the value.
//
// A note is drawn as its own full-width line, not a table row. A lipgloss
// table cannot span columns, and note text is wider than the label cell, so
// a table cell would wrap it mid-word. Splitting the table around a note is
// safe because every column here has a fixed width: the table segments
// either side of the note still line up. A caller that leaves width at zero
// (auto-sizing the value column) must not call note; only the two forms
// call it, and both set a width.
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
		// box; the pane around it owns the only border on screen.
		Border(lipgloss.Border{}).
		BorderTop(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderRow(false).BorderColumn(false).
		// BorderBottom stays on deliberately. With it off, lipgloss v1.1.0's
		// table drops the last data row entirely: three rows render as two.
		// All the glyphs are empty, so leaving it on draws nothing and costs
		// no line; turning it off costs a row.
		//
		// The fault is in table.computeHeight(), which returns one line too
		// few for a table with no headers; String() applies that as a hard
		// MaxHeight clamp. lipgloss v2.0.5 has the same arithmetic. v2 only
		// stops calling computeHeight by clamping to min(t.height,
		// computeHeight()), and t.height is 0 unless something calls
		// .Height(). Do not remove this line on the assumption v2 fixed the
		// bug: it did not, and calling .Height() on this table brings the
		// bug back, in either version. TestFieldsKeepsEveryRow guards it.
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
