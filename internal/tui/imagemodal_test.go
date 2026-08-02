package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/recipes"
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
	// The label renders in the form's VALUE column, not the whole pane, so
	// that is the width it has to fit — a row one cell over wraps.
	if w, avail := lipgloss.Width(label), formContentWidth-fieldValueColumn; w > avail {
		t.Errorf("byo label is %d cells, wider than the %d-cell value column", w, avail)
	}
}

// The BYO row is the widest the form draws — os, backend, filename, size,
// tag — so it is the one that overflows first when a column is added.
func TestImageRowsFitTheValueColumn(t *testing.T) {
	avail := formContentWidth - fieldValueColumn
	longest := imageOption{
		file:    "ubuntu-24.04-server-cloudimg-amd64-with-a-long-suffix.img",
		backend: "cloudinit",
		osName:  "ubuntu",
		bytes:   66 * 1024 * 1024, // the widest size label, "~66.0 MiB"
	}
	if w := lipgloss.Width(longest.label()); w > avail {
		t.Errorf("widest byo row is %d cells, value column is %d", w, avail)
	}

	e := iso.Entry{OS: "debian", Variant: "13 (trixie)", Backend: "cloudinit", Size: 66 * 1024 * 1024}
	catalog := imageOption{entry: &e, backend: e.Backend, osName: e.OS, bytes: e.Size}
	if w := lipgloss.Width(catalog.label()); w > avail {
		t.Errorf("widest catalog row is %d cells, value column is %d", w, avail)
	}
	if !strings.Contains(catalog.label(), "MiB") {
		t.Errorf("catalog row %q shows no size", catalog.label())
	}
}

// A BYO image used to be offered no recipes at all, ever: iso.Infer names an
// OS only for *alpine*.iso, so every qcow2/img arrives with osName "", and
// both branches of recipes.List compare against a real OS name parsed off a
// recipe filename. The fOS row is what lets the user say what the file is.
func TestBYOOSOverrideDrivesResolvedOS(t *testing.T) {
	f := formModel{images: []imageOption{{file: "mystery.qcow2", backend: "cloudinit", osName: ""}}}

	if got := f.resolvedOS(); got != "" {
		t.Fatalf("unset override resolved to %q, want empty (iso.Infer's guess)", got)
	}
	f.byoOS = "ubuntu"
	if got := f.resolvedOS(); got != "ubuntu" {
		t.Errorf("resolvedOS = %q, want the override %q", got, "ubuntu")
	}
}

// The override must not leak across images — it described the previous file.
func TestSelectImageClearsTheBYOOverrides(t *testing.T) {
	f := formModel{
		images: []imageOption{
			{file: "one.qcow2", backend: "cloudinit"},
			{file: "two.iso", backend: "ssh"},
		},
		byoOS:      "ubuntu",
		byoBackend: "cloudinit",
		recipeSel:  map[string]bool{},
	}
	f.selectImage(1)
	if f.byoOS != "" || f.byoBackend != "" {
		t.Errorf("after selectImage: byoOS=%q byoBackend=%q, want both cleared", f.byoOS, f.byoBackend)
	}
}

// The cycle must offer every OS the catalog knows, plus a way back to "unset".
func TestBYOOSNamesCoverTheCatalogAndAllowUnset(t *testing.T) {
	names := byoOSNames()
	if len(names) == 0 || names[0] != "" {
		t.Fatalf("byoOSNames = %v, want it to lead with \"\" so the override is reversible", names)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, g := range iso.ByOS() {
		if !have[g.OS] {
			t.Errorf("byoOSNames omits %q, which the catalog ships", g.OS)
		}
	}
}

// A BYO image declared to be a cloud image must record the account the seed
// actually creates. Left empty, sshx defaults to root, and cloud images lock
// root — so ssh and provisioning both fail on a VM that looks fine.
func TestBYOCloudinitConnectsAsTheSeedUser(t *testing.T) {
	f := formModel{images: []imageOption{{file: "mystery.qcow2", backend: "cloudinit", sshUser: ""}}}
	if got := f.resolvedSSHUser(); got != cloudinit.User {
		t.Errorf("resolvedSSHUser = %q, want %q — the account the seed creates", got, cloudinit.User)
	}
}

// The row only appears for BYO images; a catalog entry's OS is declared.
func TestOSRowIsBYOOnly(t *testing.T) {
	catalogEntry := iso.Entry{ID: "ubuntu-24.04", OS: "ubuntu", Variant: "24.04 LTS", Backend: "cloudinit"}
	f := formModel{images: []imageOption{{entry: &catalogEntry, backend: "cloudinit", osName: "ubuntu"}}}
	for _, focus := range f.order() {
		if focus == fOS {
			t.Error("the os override row is offered for a catalog image, whose OS is declared")
		}
	}

	f = formModel{images: []imageOption{{file: "mystery.qcow2", backend: "ssh"}}}
	var found bool
	for _, focus := range f.order() {
		if focus == fOS {
			found = true
		}
	}
	if !found {
		t.Error("the os override row is missing for a BYO image")
	}
}

// The outcome the whole row exists for: declaring the OS turns an empty
// recipe list into a real one. Goes through refreshRecipes and the installed
// recipe directory rather than asserting on resolvedOS, so it proves what the
// user actually gets.
func TestDeclaringTheOSMakesRecipesAppear(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	f := formModel{
		images:    []imageOption{{file: "mystery.qcow2", backend: "cloudinit", osName: ""}},
		recipeSel: map[string]bool{},
	}

	f.refreshRecipes()
	if len(f.recipeNames) != 0 {
		t.Fatalf("an un-declared BYO image was offered %v; expected none", f.recipeNames)
	}

	f.byoOS = "ubuntu"
	f.refreshRecipes()
	if len(f.recipeNames) == 0 {
		t.Fatal("declaring the OS still offered no recipes")
	}
	for _, n := range f.recipeNames {
		if !strings.HasSuffix(n, ".cloud.yaml") {
			t.Errorf("cloudinit backend offered %q, which is not a cloud fragment", n)
		}
	}
}

// The size column is right-aligned, and humanBytes keeps a decimal below 100
// ("66.0 MiB"), so the widest value is "~66.0 MiB" — nine cells, not the eight
// you would guess from the largest image. Undersized, the status column shifts
// on exactly the small-image rows, which is alpine-virt, the row the sizes
// exist to make comparable.
func TestSizeColumnFitsTheWidestLabel(t *testing.T) {
	cases := []imageOption{
		{bytes: 66 * 1024 * 1024},               // ~66.0 MiB — the widest
		{bytes: 595 * 1024 * 1024},              // ~595 MiB
		{bytes: 352 * 1024 * 1024, exact: true}, // 352 MiB
		{bytes: 1288490188, exact: true},        // 1.2 GiB
	}
	for _, o := range cases {
		if w := lipgloss.Width(o.sizeLabel()); w > modalSizeWidth {
			t.Errorf("sizeLabel %q is %d cells, wider than the %d-cell column",
				o.sizeLabel(), w, modalSizeWidth)
		}
	}
}

// A declared size is approximate and must say so; a stat'd one must not.
func TestSizeLabelMarksApproximations(t *testing.T) {
	declared := imageOption{bytes: 595 * 1024 * 1024}
	if got := declared.sizeLabel(); !strings.HasPrefix(got, "~") {
		t.Errorf("declared size rendered %q, want a leading ~", got)
	}
	measured := imageOption{bytes: 595 * 1024 * 1024, exact: true}
	if got := measured.sizeLabel(); strings.HasPrefix(got, "~") {
		t.Errorf("stat'd size rendered %q, want no ~", got)
	}
	if got := (imageOption{}).sizeLabel(); got != "" {
		t.Errorf("unknown size rendered %q, want empty", got)
	}
}

// Every catalog entry needs a declared size, or its row renders blank where
// every neighbouring row has a number.
func TestEveryCatalogEntryDeclaresASize(t *testing.T) {
	for _, e := range iso.Catalog() {
		if e.Size <= 0 {
			t.Errorf("catalog entry %q declares no Size", e.ID)
		}
	}
}

// A single-variant OS resolves straight from the first level, so that level is
// the only place its size is ever seen — the group has to carry it.
func TestSingleVariantGroupCarriesItsImage(t *testing.T) {
	groups := groupImages(testImages())
	for _, g := range groups {
		if len(g.idxs) == 1 && g.only.variantLabel() == "" && g.only.file == "" {
			t.Errorf("group %q has one image but does not carry it, so its size cannot render", g.os)
		}
	}
}

// The second column holds a catalog variant OR a BYO backend, and every value
// must fit it. One that does not overflows by a cell and shifts the size
// column on that row alone — which is what "13 (trixie)" did against a
// 10-cell column, and which no width test catches, because the ROW still fits
// the pane. Only the alignment breaks.
func TestImageMetaColumnFitsEveryValue(t *testing.T) {
	for _, e := range iso.Catalog() {
		if w := lipgloss.Width(e.Variant); w > imageMetaWidth {
			t.Errorf("catalog variant %q is %d cells, column is %d — its size will misalign",
				e.Variant, w, imageMetaWidth)
		}
	}
	for _, b := range byoBackends {
		if w := lipgloss.Width(b); w > imageMetaWidth {
			t.Errorf("backend %q is %d cells, column is %d", b, w, imageMetaWidth)
		}
	}
}

// byoVariants drills the modal into the byo group and returns it positioned
// at the variant level, where choose an image… lives.
func byoVariants(t *testing.T, mo *imageModal) {
	t.Helper()
	for i, g := range mo.groups {
		if g.os == byoGroup {
			mo.list.Select(i)
		}
	}
	if _, _, closed := mo.update(keyMsg("enter")); closed {
		t.Fatal("enter on the byo group closed the modal instead of drilling in")
	}
	if mo.level != levelVariant || mo.osName != byoGroup {
		t.Fatalf("after enter: level=%v os=%q, want variant/%q", mo.level, mo.osName, byoGroup)
	}
}

// choose an image… must be reachable via the normal drill-in even though
// testImages gives the byo group exactly one real file — the single-variant
// shortcut that resolves other one-image groups straight from the OS level
// would otherwise make the entry unreachable whenever there's only one BYO
// file (or none at all).
func TestChooseImageLeafReachableWithASingleBYOFile(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	byoVariants(t, mo)

	items := mo.list.Items()
	last, ok := items[len(items)-1].(variantItem)
	if !ok || !last.chooseImage {
		t.Fatalf("last item in the byo group's variant list is %#v, want the choose an image… entry", items[len(items)-1])
	}
}

// The real invariant the column width serves: every catalog row puts its size
// in the same place.
func TestCatalogRowsAlignTheirSizeColumn(t *testing.T) {
	var want int
	for i, e := range iso.Catalog() {
		e := e
		o := imageOption{entry: &e, backend: e.Backend, osName: e.OS, bytes: e.Size}
		// Everything before the size is fixed-width, so its width is where the
		// size column starts.
		prefix := lipgloss.Width(fmt.Sprintf("%-8s %-*s", e.OS, imageMetaWidth, e.Variant))
		if i == 0 {
			want = prefix
			continue
		}
		if prefix != want {
			t.Errorf("row %q starts its size at column %d, others at %d", o.label(), prefix, want)
		}
	}
}

// The whole point of the mode: type part of a name and the non-matching
// images go away. Asserted on drawn output, not on the model's item slice --
// a filter that matches but does not redraw is the bug this catches.
func TestFinderFiltersAsYouType(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.byo = true
	mo.setFound([]foundImage{
		{path: "/home/u/downloads/alpine-3.20.iso", size: 100},
		{path: "/home/u/vms/ubuntu-24.04.iso", size: 200},
		{path: "/home/u/vms/debian-12.qcow2", size: 300},
	})

	out := ansi.Strip(mo.view())
	for _, want := range []string{"alpine", "ubuntu", "debian"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the finder does not list %s before filtering:\n%s", want, out)
		}
	}

	mo.byoList.SetFilterText("ubu")
	out = ansi.Strip(mo.view())
	if !strings.Contains(out, "ubuntu") {
		t.Errorf("filtering for \"ubu\" hid the match:\n%s", out)
	}
	for _, gone := range []string{"alpine", "debian"} {
		if strings.Contains(out, gone) {
			t.Errorf("filtering for \"ubu\" still shows %s:\n%s", gone, out)
		}
	}
}

// Fuzzy, not substring: "u2404" must find "ubuntu-24.04.iso". This is the
// difference between bubbles/list's filter and huh's, and the reason the
// list component was chosen at all.
func TestFinderMatchesFuzzilyNotJustSubstrings(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.byo = true
	mo.setFound([]foundImage{
		{path: "/home/u/vms/ubuntu-24.04.iso", size: 100},
		{path: "/home/u/vms/alpine-3.20.iso", size: 200},
	})

	mo.byoList.SetFilterText("u2404")
	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "ubuntu") {
		t.Errorf("a fuzzy query did not match; the filter is substring-only:\n%s", out)
	}
}

// Results stream in while the walk is still running, so the pane must say so
// -- an empty box with no explanation reads as "there are no images".
func TestFinderSaysItIsStillLooking(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.byo = true

	if out := ansi.Strip(mo.view()); !strings.Contains(out, "searching") {
		t.Errorf("nothing tells the user a scan is running:\n%s", out)
	}

	mo.setFound([]foundImage{{path: "/home/u/a.iso", size: 1}})
	mo.scanDone = true
	if out := ansi.Strip(mo.view()); strings.Contains(out, "searching") {
		t.Errorf("the finder still claims to be searching after the scan ended:\n%s", out)
	}
}

// A finished scan that found nothing must say THAT, rather than sitting
// blank, or the user waits for results that are never coming.
func TestFinderSaysWhenItFoundNothing(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.byo = true
	mo.scanDone = true

	if out := ansi.Strip(mo.view()); !strings.Contains(out, "no disk images") {
		t.Errorf("a scan that found nothing says nothing:\n%s", out)
	}
}

// esc goes back a level rather than closing the modal -- the byo screen
// follows the same rule as every other sub-mode here, because the user is
// one step into a choice and dumping them out would discard it.
func TestByoEscGoesBackToTheVariantList(t *testing.T) {
	imgs := testImages()
	mo := newImageModal(imgs, 0)
	mo.drill(mo.groups[len(mo.groups)-1]) // byo group, where choose an image… lives
	mo.openByo()

	_, idx, closed := mo.update(keyMsg("esc"))
	if closed {
		t.Error("esc closed the whole modal instead of leaving the byo screen")
	}
	if idx != -1 {
		t.Errorf("esc chose image %d", idx)
	}
	if mo.byo {
		t.Error("esc did not leave the byo screen")
	}

	// State alone would pass even if view() still drew the byo screen --
	// assert on what is actually redrawn.
	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "choose an image…") {
		t.Errorf("esc left the byo screen in state but not on screen; drawn:\n%s", out)
	}
	if strings.Contains(out, "searching") {
		t.Errorf("the byo screen is still drawn after esc:\n%s", out)
	}
}

// Re-entering the finder must start clean: leaving it (esc) does not cancel
// the running scan by itself -- if the old scan's messages keep landing (see
// TestReEnteringFinderDoesNotDoubleResults), every result would be appended
// twice, adjacent, because the old list's items survive across openByo.
func TestReEnteringByoDoesNotDoubleResults(t *testing.T) {
	imgs := testImages()
	mo := newImageModal(imgs, 0)
	mo.drill(mo.groups[len(mo.groups)-1])
	mo.openByo()
	mo.setFound([]foundImage{{path: "/home/u/only.iso", size: 1}})

	mo.update(keyMsg("esc"))                                       // leave the byo screen
	mo.openByo()                                                   // re-enter: must not carry the old results over
	mo.setFound([]foundImage{{path: "/home/u/only.iso", size: 1}}) // the new scan finds it again

	out := ansi.Strip(mo.view())
	if n := strings.Count(out, "only.iso"); n != 1 {
		t.Errorf("only.iso appears %d times after re-entering the finder, want 1:\n%s", n, out)
	}
}

// Choosing a found image must produce a real BYO option appended to the
// modal's own slice -- app.go re-adopts mo.images and resolves the returned
// index against it.
func TestByoChoosingAppendsTheImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.iso")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mo := newImageModal(testImages(), 0)
	before := len(mo.images)
	mo.byo = true
	mo.setFound([]foundImage{{path: path, size: 1}})

	_, idx, closed := mo.update(keyMsg("enter"))
	if !closed {
		t.Fatal("choosing an image did not close the modal")
	}
	if idx != before {
		t.Fatalf("index = %d, want %d (appended past the original bounds)", idx, before)
	}
	if len(mo.images) != before+1 {
		t.Fatalf("the chosen image was not appended: %d images", len(mo.images))
	}
	if got, err := mo.images[idx].imagePath(); err != nil || got != path {
		t.Errorf("appended path = %q, err = %v, want %q", got, err, path)
	}
}

// The scan pumps itself: every batch message carries the command that fetches
// the next one. A gate that drops non-key messages does not merely delay
// results, it ENDS the chain -- so drive it through the real app.Update, not
// the modal in isolation.
func TestScanResultsReachTheByoScreenThroughTheApp(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	m := model{screen: screenList, list: newVMList(), provisioning: map[string]provState{}}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(model)

	m.modal = newImageModal(testImages(), 0)
	m.modal.byo = true

	mm, _ = m.Update(imagesFoundMsg{batch: []foundImage{
		{path: "/home/u/vms/found-by-scan.iso", size: 10},
	}})
	m = mm.(model)

	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "found-by-scan") {
		t.Errorf("a scan result never reached the byo screen:\n%s", out)
	}
}

// And the pump must keep going: the command returned alongside the batch is
// what fetches the next one.
func TestScanKeepsPumpingAfterABatch(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	m := model{screen: screenList, list: newVMList(), provisioning: map[string]provState{}}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(model)

	ch := make(chan []foundImage, 1)
	ch <- []foundImage{{path: "/home/u/second.iso", size: 1}}
	m.modal = newImageModal(testImages(), 0)
	m.modal.byo = true
	m.modal.scanCh = ch

	_, cmd := m.Update(imagesFoundMsg{batch: []foundImage{{path: "/home/u/first.iso", size: 1}}})
	if cmd == nil {
		t.Fatal("a batch did not re-issue the command that fetches the next one")
	}

	// Execute it: a pump re-issued against the wrong channel would still be
	// non-nil, so only running it and checking what it actually reads proves
	// it points at THIS scan's channel.
	next := cmd()
	found, ok := next.(imagesFoundMsg)
	if !ok {
		t.Fatalf("re-issued command produced %T, want imagesFoundMsg", next)
	}
	if len(found.batch) != 1 || found.batch[0].path != "/home/u/second.iso" {
		t.Errorf("re-issued command read the wrong channel; batch = %v, want second.iso", found.batch)
	}
}

// Choosing choose an image… must swap the variant list for a focused text
// field over a result list -- asserted on the drawn output, not the mode
// flag, since a flag that flips without a redraw is invisible to the user.
func TestChoosingByoDrawsAFocusedInput(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	byoVariants(t, mo)
	mo.list.Select(len(mo.list.Items()) - 1) // choose an image…, trailing the real BYO file

	if _, _, closed := mo.update(keyMsg("enter")); closed {
		t.Fatal("enter on choose an image… closed the modal")
	}
	if !mo.byo {
		t.Fatal("enter on choose an image… did not open the byo screen")
	}
	if !mo.byoInput.Focused() {
		t.Error("the byo text field was not focused")
	}

	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "type to search") {
		t.Errorf("the byo field's placeholder is not on screen:\n%s", out)
	}
}

// Arrow keys must move the list's selection, not the text field -- and the
// DRAWN highlight must follow, since a cursor that moves in state but not on
// screen is invisible to the user.
func TestArrowKeysMoveTheByoSelectionAndTheHighlightFollows(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.openByo()
	mo.setFound([]foundImage{
		{path: "/home/u/alpine.iso", size: 1},
		{path: "/home/u/debian.iso", size: 2},
	})

	if it, ok := mo.byoList.SelectedItem().(foundItem); !ok || !strings.HasSuffix(it.img.path, "alpine.iso") {
		t.Fatalf("selection starts on %v, want alpine.iso", mo.byoList.SelectedItem())
	}

	mo.update(keyMsg("down"))
	it, ok := mo.byoList.SelectedItem().(foundItem)
	if !ok || !strings.HasSuffix(it.img.path, "debian.iso") {
		t.Fatalf("down did not move the selection to debian.iso: %v", mo.byoList.SelectedItem())
	}
	out := ansi.Strip(mo.view())
	var debianLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "debian") {
			debianLine = line
		}
	}
	if !strings.Contains(debianLine, glyphCursor) {
		t.Errorf("debian's row is not drawn as selected after down:\n%s", out)
	}

	mo.update(keyMsg("up"))
	if it, ok := mo.byoList.SelectedItem().(foundItem); !ok || !strings.HasSuffix(it.img.path, "alpine.iso") {
		t.Fatalf("up did not move the selection back to alpine.iso: %v", mo.byoList.SelectedItem())
	}
}

// Up/down must reach the list rather than being swallowed by the focused
// text field -- bubbles/textinput ignores them, but this is the contract
// that matters, not an assumption about the component's own key handling.
func TestArrowKeysDoNotLeakIntoTheByoTextField(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.openByo()
	mo.setFound([]foundImage{{path: "/home/u/alpine.iso", size: 1}})

	before := mo.byoInput.Value()
	mo.update(keyMsg("down"))
	mo.update(keyMsg("up"))
	if mo.byoInput.Value() != before {
		t.Errorf("arrow keys changed the text field's value: %q, want %q", mo.byoInput.Value(), before)
	}
}

// Typing a real path and pressing enter must select it even with no results
// list match -- this is how an image OUTSIDE $HOME (never found by the scan)
// gets chosen now that the tree browser is gone.
func TestTypingARealPathSelectsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.qcow2")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mo := newImageModal(testImages(), 0)
	before := len(mo.images)
	mo.openByo()
	mo.byoInput.SetValue(path)

	_, idx, closed := mo.update(keyMsg("enter"))
	if !closed {
		t.Fatal("choosing a typed path did not close the modal")
	}
	if idx != before {
		t.Fatalf("index = %d, want %d (appended past the original bounds)", idx, before)
	}
	if len(mo.images) != before+1 {
		t.Fatalf("the typed path was not appended: %d images", len(mo.images))
	}
	if got, err := mo.images[idx].imagePath(); err != nil || got != path {
		t.Errorf("appended path = %q, err = %v, want %q", got, err, path)
	}
}

// ~ and ~/... must expand to the user's home directory before the path is
// resolved -- typed paths are the one entry point in this modal that takes
// free text, so it's the one place a user is likely to type it.
func TestTypingATildePathExpands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "custom.iso")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mo := newImageModal(testImages(), 0)
	mo.openByo()
	mo.byoInput.SetValue("~/custom.iso")

	_, idx, closed := mo.update(keyMsg("enter"))
	if !closed {
		t.Fatal("choosing a ~-path did not close the modal")
	}
	if got, err := mo.images[idx].imagePath(); err != nil || got != path {
		t.Errorf("appended path = %q, err = %v, want %q", got, err, path)
	}
}

// A path that doesn't exist must stay open and say why, right on screen --
// not close the modal, not silently do nothing.
func TestTypingANonexistentPathShowsAnErrorAndStaysOpen(t *testing.T) {
	dir := t.TempDir()
	mo := newImageModal(testImages(), 0)
	before := len(mo.images)
	mo.openByo()
	mo.byoInput.SetValue(filepath.Join(dir, "nope.iso"))

	_, idx, closed := mo.update(keyMsg("enter"))
	if closed {
		t.Fatal("a bad path closed the modal")
	}
	if idx != -1 {
		t.Fatalf("a bad path resolved an index: %d", idx)
	}
	if len(mo.images) != before {
		t.Fatalf("a bad path was appended anyway: %d images", len(mo.images))
	}
	if !mo.byo {
		t.Fatal("a bad path left the byo screen")
	}

	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "no such file") && !strings.Contains(out, "nope.iso") {
		t.Errorf("the error is not drawn on screen:\n%s", out)
	}
}

// A directory must also be rejected, with the reason drawn -- byoOptionFromPath
// is the authority on this rule, and it is doing it right below.
func TestTypingADirectoryShowsAnErrorAndStaysOpen(t *testing.T) {
	dir := t.TempDir()
	mo := newImageModal(testImages(), 0)
	mo.openByo()
	mo.byoInput.SetValue(dir)

	_, _, closed := mo.update(keyMsg("enter"))
	if closed {
		t.Fatal("a directory closed the modal")
	}
	if !mo.byo {
		t.Fatal("a directory left the byo screen")
	}

	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "directory") {
		t.Errorf("the directory error is not drawn on screen:\n%s", out)
	}
}

// Empty input plus enter, with no result selected, must be a no-op: no
// crash, no close.
func TestTypingEmptyPathDoesNothing(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.openByo()

	_, idx, closed := mo.update(keyMsg("enter"))
	if closed {
		t.Fatal("enter on an empty path field closed the modal")
	}
	if idx != -1 {
		t.Fatalf("enter on an empty path field resolved an index: %d", idx)
	}
	if !mo.byo {
		t.Fatal("enter on an empty path field left the byo screen")
	}
}

// esc from the byo screen must return to the variant list, not out of the
// modal -- same rule every sub-mode here follows, asserted on what's
// actually redrawn.
func TestEscWhileByoReturnsToVariantList(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	byoVariants(t, mo)
	mo.list.Select(len(mo.list.Items()) - 1) // choose an image…, trailing the real BYO file
	if _, _, closed := mo.update(keyMsg("enter")); closed {
		t.Fatal("enter on choose an image… closed the modal")
	}

	_, _, closed := mo.update(keyMsg("esc"))
	if closed {
		t.Error("esc while on the byo screen closed the modal; it must return to the variant list")
	}
	if mo.byo {
		t.Error("esc left mo.byo true; the screen should be dismissed")
	}
	if mo.level != levelVariant {
		t.Errorf("esc while on the byo screen left level=%v, want the variant level", mo.level)
	}

	out := ansi.Strip(mo.view())
	if !strings.Contains(out, "choose an image…") {
		t.Errorf("esc left the byo screen in state but not on screen; drawn:\n%s", out)
	}
}

// The non-key message gate in app.go must cover the byo screen, or neither
// the scan's batches nor the field's cursor blink ever reach it -- see
// 81cbd92 for the shape of bug this guards against (a swallowed non-key
// message that ended a chain rather than stalling it).
func TestByoReceivesNonKeyMessages(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	m := model{screen: screenForm, form: newForm()}
	m.form.fetching = true
	m.modal = newImageModal(testImages(), 0)
	m.modal.openByo()

	_, cmd := m.Update(dlTickMsg{})
	if cmd == nil {
		t.Fatal("dlTickMsg while on the byo screen returned no cmd: the download tick chain is dead")
	}
}
