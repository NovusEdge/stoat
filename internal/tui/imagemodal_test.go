package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/iso"
)

// testImages is a fixture with the shape that motivated the modal: one OS
// with two variants, several with one, and a BYO file whose OS iso.Infer
// could not name.
func testImages() []imageOption {
	std := iso.Entry{ID: "alpine-standard", OS: "alpine", Variant: "standard", Backend: "apkovl"}
	virt := iso.Entry{ID: "alpine-virt", OS: "alpine", Variant: "virt", Backend: "apkovl"}
	ubu := iso.Entry{ID: "ubuntu-24.04", OS: "ubuntu", Variant: "24.04 LTS", Backend: "cloudinit"}
	return []imageOption{
		{entry: &std, backend: "apkovl", osName: "alpine", file: "alpine-standard-3.24.1-x86_64.iso"},
		{entry: &virt, backend: "apkovl", osName: "alpine"},
		{entry: &ubu, backend: "cloudinit", osName: "ubuntu"},
		{file: "some-unrecognised-thing.qcow2", backend: "ssh", osName: ""},
	}
}

func TestGroupImagesBucketsByOSAndKeepsOrder(t *testing.T) {
	groups := groupImages(testImages())

	var got []string
	for _, g := range groups {
		got = append(got, g.os)
	}
	want := []string{"alpine", "ubuntu", byoGroup}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("groups = %v, want %v (catalog order, byo last)", got, want)
	}

	// Every image reachable exactly once, or an image becomes unselectable
	// while the menu still looks complete.
	seen := map[int]int{}
	for _, g := range groups {
		for _, idx := range g.idxs {
			seen[idx]++
		}
	}
	for i := range testImages() {
		if seen[i] != 1 {
			t.Errorf("image %d appears %d times across groups, want 1", i, seen[i])
		}
	}
	if len(groups[0].idxs) != 2 {
		t.Errorf("alpine group has %d images, want 2 (standard and virt)", len(groups[0].idxs))
	}
}

// TestModalDrillsAndReturnsTheRightIndex is the core contract: whatever the
// user lands on must map back to the right entry in the form's flat slice.
func TestModalDrillsAndReturnsTheRightIndex(t *testing.T) {
	imgs := testImages()
	mo := newImageModal(imgs, 0)

	if mo.level != levelOS {
		t.Fatal("modal did not open at the OS level")
	}

	// alpine is first and has two variants, so enter drills rather than
	// choosing.
	_, chosen, closed := mo.update(keyMsg("enter"))
	if chosen != -1 || closed {
		t.Fatalf("enter on a multi-variant OS chose %d / closed %v, want -1 / false", chosen, closed)
	}
	if mo.level != levelVariant || mo.osName != "alpine" {
		t.Fatalf("after enter: level=%v os=%q, want variant/alpine", mo.level, mo.osName)
	}

	// Second variant is alpine-virt, which is index 1 in the flat slice.
	mo.list.Select(1)
	_, chosen, closed = mo.update(keyMsg("enter"))
	if chosen != 1 {
		t.Errorf("chose index %d, want 1 (alpine-virt)", chosen)
	}
	if !closed {
		t.Error("choosing a variant did not close the modal")
	}
}

// A one-image OS has nothing to choose between, so it resolves straight from
// the first level rather than making the user press enter twice for no
// decision.
func TestModalSkipsTheSecondLevelForASingleVariant(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.list.Select(1) // ubuntu, one variant, index 2 in the flat slice

	_, chosen, closed := mo.update(keyMsg("enter"))
	if chosen != 2 {
		t.Errorf("chose %d, want 2 (ubuntu)", chosen)
	}
	if !closed {
		t.Error("modal stayed open on a single-variant OS")
	}
}

func TestModalEscGoesBackALevelThenCloses(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.update(keyMsg("enter")) // into alpine

	_, _, closed := mo.update(keyMsg("esc"))
	if closed {
		t.Error("esc closed the modal from the variant level; it should go back a level")
	}
	if mo.level != levelOS {
		t.Errorf("esc left level=%v, want the OS level", mo.level)
	}
	// The cursor must land back on the group we came from, not the top.
	if g, ok := mo.list.SelectedItem().(osItem); !ok || g.os != "alpine" {
		t.Error("esc did not restore the cursor to the group it drilled out of")
	}

	if _, _, closed = mo.update(keyMsg("esc")); !closed {
		t.Error("esc at the OS level did not close the modal")
	}
}

// TestModalOpensOnTheCurrentSelection: opening the picker must not silently
// move the user somewhere else in the catalog.
func TestModalOpensOnTheCurrentSelection(t *testing.T) {
	mo := newImageModal(testImages(), 2) // ubuntu
	g, ok := mo.list.SelectedItem().(osItem)
	if !ok || g.os != "ubuntu" {
		t.Errorf("opened on %v, want the ubuntu group", mo.list.SelectedItem())
	}
}

// TestModalFitsTheSmallestTerminal is the geometry check. A modal's failure
// mode is that it renders fine at the developer's window and corrupts at
// someone else's, and app.go still draws panes at 60x20.
func TestModalFitsTheSmallestTerminal(t *testing.T) {
	m := model{width: smallWidth, height: smallHeight, modal: newImageModal(testImages(), 0)}

	for _, level := range []string{"os", "variant"} {
		if level == "variant" {
			m.modal.update(keyMsg("enter"))
		}
		box := m.modal.view()
		if w := lipgloss.Width(box); w > smallWidth {
			t.Errorf("%s level: modal is %d cells wide, terminal is %d", level, w, smallWidth)
		}
		if h := lipgloss.Height(box); h > smallHeight {
			t.Errorf("%s level: modal is %d lines tall, terminal is %d", level, h, smallHeight)
		}

		// The composited frame must not grow the screen it is drawn over.
		screen := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, "")
		out := m.renderModal(screen)
		if w := lipgloss.Width(out); w > m.width {
			t.Errorf("%s level: composited frame is %d cells wide, terminal is %d", level, w, m.width)
		}
		if h := lipgloss.Height(out); h > m.height {
			t.Errorf("%s level: composited frame is %d lines tall, terminal is %d", level, h, m.height)
		}
	}
}

// A long BYO filename is the one label here with no upper bound, so it is the
// one that can push the box out of square.
func TestModalTruncatesLongBYOFilenames(t *testing.T) {
	imgs := []imageOption{{
		file:    "ubuntu-24.04-server-cloudimg-amd64-with-a-very-long-suffix.img",
		backend: "ssh",
	}}
	mo := newImageModal(imgs, 0)
	if w := lipgloss.Width(mo.view()); w > modalContentWidth+paneFrame() {
		t.Errorf("modal grew to %d cells for a long filename, want <= %d", w, modalContentWidth+paneFrame())
	}
}

// The form's own image row has the same unbounded-filename problem, and used
// to render an unnamed OS as a bare "?".
func TestBYOLabelIsBoundedAndNamesTheUnknown(t *testing.T) {
	o := imageOption{file: "a-really-quite-long-image-filename-indeed.qcow2", backend: "ssh"}
	label := o.label()
	if !strings.Contains(label, osUnknown) {
		t.Errorf("label %q does not say %q for an OS iso.Infer could not name", label, osUnknown)
	}
	if strings.Contains(label, "?") {
		t.Errorf("label %q still renders the unnamed OS as a bare question mark", label)
	}
	if w := lipgloss.Width(label); w > formContentWidth {
		t.Errorf("byo label is %d cells, wider than the form's %d-cell pane", w, formContentWidth)
	}
}
