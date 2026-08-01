package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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
// at the variant level, where browse… lives.
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

// AllowedTypes must actually filter: a .txt sitting next to an .iso is not a
// bootable image and must not be offerable.
func TestPickerFiltersToImageTypes(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.openBrowser()
	want := []string{".iso", ".qcow2", ".img"}
	if !reflect.DeepEqual(mo.picker.AllowedTypes, want) {
		t.Errorf("AllowedTypes = %v, want %v", mo.picker.AllowedTypes, want)
	}
	if mo.picker.AutoHeight {
		t.Error("AutoHeight must be false; the modal assigns the rect")
	}
}

// Same rule as every other pane: nothing drawn may exceed the box it is in.
// Mirrors TestModalFitsTheSmallestTerminal's 60x20 check.
func TestPickerFitsTheModalAtMinimumSize(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	mo.openBrowser()
	m := model{width: smallWidth, height: smallHeight, modal: mo}

	box := m.modal.view()
	if w := lipgloss.Width(box); w > smallWidth {
		t.Errorf("browsing: modal is %d cells wide, terminal is %d", w, smallWidth)
	}
	if h := lipgloss.Height(box); h > smallHeight {
		t.Errorf("browsing: modal is %d lines tall, terminal is %d", h, smallHeight)
	}

	screen := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, "")
	out := m.renderModal(screen)
	if w := lipgloss.Width(out); w > m.width {
		t.Errorf("browsing: composited frame is %d cells wide, terminal is %d", w, m.width)
	}
	if h := lipgloss.Height(out); h > m.height {
		t.Errorf("browsing: composited frame is %d lines tall, terminal is %d", h, m.height)
	}
}

// browse… must be reachable via the normal drill-in even though testImages
// gives the byo group exactly one real file — the single-variant shortcut
// that resolves other one-image groups straight from the OS level would
// otherwise make the entry unreachable whenever there's only one BYO file
// (or none at all).
func TestBrowseEntryReachableWithASingleBYOFile(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	byoVariants(t, mo)

	items := mo.list.Items()
	last, ok := items[len(items)-1].(variantItem)
	if !ok || !last.browse {
		t.Fatalf("last item in the byo group's variant list is %#v, want the browse… entry", items[len(items)-1])
	}
}

// esc while browsing returns to the variant list, not the whole modal --
// closing outright would be jarring mid-browse, and esc already means "back
// a level" everywhere else in this modal.
func TestEscWhileBrowsingReturnsToVariantList(t *testing.T) {
	mo := newImageModal(testImages(), 0)
	byoVariants(t, mo)

	items := mo.list.Items()
	mo.list.Select(len(items) - 1) // browse…
	if _, _, closed := mo.update(keyMsg("enter")); closed {
		t.Fatal("enter on browse… closed the modal")
	}
	if !mo.browsing {
		t.Fatal("enter on browse… did not open the picker")
	}

	_, _, closed := mo.update(keyMsg("esc"))
	if closed {
		t.Error("esc while browsing closed the modal; it must return to the variant list")
	}
	if mo.browsing {
		t.Error("esc while browsing left mo.browsing true; the picker should be dismissed")
	}
	if mo.level != levelVariant {
		t.Errorf("esc while browsing left level=%v, want the variant level", mo.level)
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
