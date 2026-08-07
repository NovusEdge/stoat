package tui

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/recipes"
)

// TestFormTabOrder pins tab focus to the order viewForm renders fields in
// (name, iso, mode, ram, cpus, [disk], share), not the field constants'
// declaration order (name, ram, cpus, disk, share, iso, mode). In live mode,
// tab must not land on fDisk: viewForm renders no disk row, and no "❯"
// marker, in that mode, so a keystroke there would silently edit an
// invisible field. The test checks the exact visited sequence, forward and
// backward, in both modes, including the wrap at both ends.
func TestFormTabOrder(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		order []int // visual/traversal order, starting from the initial focus (fName)
	}{
		{"live", "live", []int{fName, fISO, fMode, fRAM, fCPUs, fShare, fRecipes}},
		{"disk", "disk", []int{fName, fISO, fMode, fRAM, fCPUs, fDisk, fShare, fRecipes}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := model{form: newForm()}
			m.form.mode = c.mode

			if m.form.focus != fName {
				t.Fatalf("initial focus = %d, want fName (%d)", m.form.focus, fName)
			}

			// Forward: tab len(order) times should visit every position in
			// order and land back on the start (fName), one full wrap.
			var forward []int
			for i := 0; i < len(c.order); i++ {
				mm, _ := m.updateForm(keyMsg("tab"))
				m = mm.(model)
				forward = append(forward, m.form.focus)
			}
			wantForward := append(append([]int{}, c.order[1:]...), c.order[0])
			if !reflect.DeepEqual(forward, wantForward) {
				t.Fatalf("tab sequence = %v, want %v", forward, wantForward)
			}
			if m.form.focus != fName {
				t.Fatalf("after full forward cycle, focus = %d, want fName (%d)", m.form.focus, fName)
			}

			// Backward: shift+tab len(order) times from fName should retrace
			// the same order in reverse and land back on fName, one full
			// wrap the other way.
			var backward []int
			for i := 0; i < len(c.order); i++ {
				mm, _ := m.updateForm(keyMsg("shift+tab"))
				m = mm.(model)
				backward = append(backward, m.form.focus)
			}
			wantBackward := make([]int, len(c.order))
			for i := range c.order {
				// reverse(order) rotated to start right before fName
				wantBackward[i] = c.order[(len(c.order)-1-i)%len(c.order)]
			}
			if !reflect.DeepEqual(backward, wantBackward) {
				t.Fatalf("shift+tab sequence = %v, want %v", backward, wantBackward)
			}
			if m.form.focus != fName {
				t.Fatalf("after full backward cycle, focus = %d, want fName (%d)", m.form.focus, fName)
			}

			if c.mode == "live" {
				for _, fv := range append(forward, backward...) {
					if fv == fDisk {
						t.Fatalf("fDisk visited in live mode, sequence forward=%v backward=%v", forward, backward)
					}
				}
			} else {
				found := false
				for _, f := range forward {
					if f == fDisk {
						found = true
					}
				}
				if !found {
					t.Fatalf("fDisk not visited in disk mode forward sequence %v", forward)
				}
			}
		})
	}
}

// build is the VM the form describes. core decides what that VM is, so the
// form's own job ends at spec(); build runs core.Plan on top of it, the
// same as the enter key does.
func (f formModel) build() (*config.VM, error) {
	s, err := f.spec()
	if err != nil {
		return nil, err
	}
	return core.Plan(s)
}

// stubImage puts a real file under isos/ and returns the picker option for
// it. core resolves an image against the filesystem, so a made-up filename
// does not build.
func stubImage(t *testing.T, name string) imageOption {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(config.Root(), "isos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Root(), "isos", name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend, osName := iso.Infer(name)
	return imageOption{file: name, backend: backend, osName: osName}
}

// TestBuildAssignsSelectedRecipes checks that build() carries exactly the
// checked recipe names into the VM, in recipeNames order. An empty
// selection must still build, with an empty Recipes slice, not a blocked
// build or a nil-panic.
func TestBuildAssignsSelectedRecipes(t *testing.T) {
	newBuildableForm := func(t *testing.T, name string) formModel {
		t.Helper()
		t.Setenv("STOAT_HOME", t.TempDir())
		f := newForm()
		f.inputs[fName].SetValue(name)
		f.images = []imageOption{stubImage(t, "alpine-standard-3.20.0-x86_64.iso")}
		f.imgIdx = 0
		// core.Plan refuses a recipe name that is not available for the VM's
		// OS and backend, so these must be real names from recipes.List, not
		// synthetic ones like "alpha". The test still checks that the
		// checked subset carries through in recipeNames order, not
		// selection order.
		if err := recipes.Install(); err != nil {
			t.Fatal(err)
		}
		f.recipeNames = []string{"devtools", "docker", "xfce"}
		return f
	}

	t.Run("selected recipes carried through", func(t *testing.T) {
		f := newBuildableForm(t, "withrecipes")
		f.recipeSel = map[string]bool{"xfce": true, "devtools": true}

		vm, err := f.build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := []string{"devtools", "xfce"} // recipeNames order, not selection order
		if !reflect.DeepEqual(vm.Recipes, want) {
			t.Fatalf("Recipes = %v, want %v", vm.Recipes, want)
		}
	})

	t.Run("no recipes selected still builds", func(t *testing.T) {
		f := newBuildableForm(t, "norecipes")

		vm, err := f.build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if len(vm.Recipes) != 0 {
			t.Fatalf("Recipes = %v, want empty", vm.Recipes)
		}
	})
}

// TestBuildRejectsRelativeDiskSize checks that build() refuses a relative
// disk size like "+8G". qemu-img's resize reads a leading "+" as "grow by",
// not "resize to", which would silently double a fresh overlay if the value
// reached it unchecked. edit.go's parseSize already refuses this on the
// edit path; build() must refuse it the same way, returning an error that
// the caller surfaces as m.form.err.
func TestBuildRejectsRelativeDiskSize(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	f := newForm()
	f.inputs[fName].SetValue("relsize")
	f.images = []imageOption{stubImage(t, "alpine-standard-3.20.0-x86_64.iso")}
	f.imgIdx = 0
	f.mode = "disk"
	f.inputs[fDisk].SetValue("+8G")

	_, err := f.build()
	if err == nil {
		t.Fatal("expected build() to reject a relative disk size (+8G)")
	}
}

// TestFormAndParseSizeAgreeOnDiskStrings asserts build()'s disk validation and
// core.ParseSize accept and reject the exact same strings, so the two
// validators cannot silently drift apart and disagree about what the same
// disk size means.
func TestFormAndParseSizeAgreeOnDiskStrings(t *testing.T) {
	cases := []string{
		"8G", "512M", "1T", "1024K", // valid
		"+8G", "-8G", // relative, refused
		"8Gigs", "", "abc", "0G", "-1G", // otherwise invalid
	}

	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			t.Setenv("STOAT_HOME", t.TempDir())
			f := newForm()
			f.inputs[fName].SetValue("agree")
			f.images = []imageOption{stubImage(t, "alpine-standard-3.20.0-x86_64.iso")}
			f.imgIdx = 0
			f.mode = "disk"
			f.inputs[fDisk].SetValue(s)

			_, buildErr := f.build()
			_, sizeErr := core.ParseSize(s)

			// Empty is a special case on the form: it means "use the default",
			// not "invalid": parseSize alone would refuse it, but build() must
			// not, so the two are only compared when the string isn't empty.
			if s == "" {
				if buildErr != nil {
					t.Fatalf("build() rejected empty disk size, want default: %v", buildErr)
				}
				return
			}

			if (buildErr == nil) != (sizeErr == nil) {
				t.Fatalf("disagreement on %q: build() err=%v, parseSize err=%v", s, buildErr, sizeErr)
			}
		})
	}
}

// TestFormPaneWidthIsStable checks that the pane does not resize, or
// re-center, when the download block appears. pane() hugs its content, and
// the download stats line is wider than any form row, so without this the
// whole pane jumped mid-download.
func TestFormPaneWidthIsStable(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenForm, width: 100, height: 40, form: newForm()}
	idle := lipgloss.Width(m.viewForm())

	m.form.fetching = true
	m.form.fetchingOS = "ubuntu"
	m.form.dl = dlStats{done: 600 << 20, total: 1200 << 20, elapsed: 71 * time.Second}
	busy := lipgloss.Width(m.viewForm())

	if idle != busy {
		t.Errorf("pane width changed when the download appeared: %d -> %d", idle, busy)
	}
}

// TestSpaceDownloadsOnImageRow covers the key move: space starts the
// download on the image row (and re-downloads an image that is already
// local), while still toggling recipes on the recipes row.
func TestSpaceDownloadsOnImageRow(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	space := keyMsg(keySpace)

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fISO
	got, cmd := m.Update(space)
	if !got.(model).form.fetching {
		t.Error("space on the image row did not start a download")
	}
	if cmd == nil {
		t.Error("space on the image row returned no command")
	}

	// A second press while one is in flight must not start another.
	again, _ := got.(model).Update(space)
	if !again.(model).form.fetching {
		t.Error("in-flight download was cancelled by a second space")
	}

	// The recipes row still toggles.
	r := model{screen: screenForm, form: newForm()}
	r.form.focus = fRecipes
	if len(r.form.recipeNames) > 0 {
		name := r.form.recipeNames[r.form.recipeIdx]
		out, _ := r.Update(space)
		if !out.(model).form.recipeSel[name] {
			t.Errorf("space on the recipes row did not toggle %q", name)
		}
		if out.(model).form.fetching {
			t.Error("space on the recipes row started a download")
		}
	}
}

// TestEnterDoesNotDownload pins the move of downloading off enter: enter on
// an undownloaded image must report what to do rather than silently starting
// a multi-hundred-megabyte fetch.
func TestEnterDoesNotDownload(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fISO
	// Force the selected image to look not-yet-downloaded.
	m.form.images = []imageOption{{entry: &iso.Entry{ID: "x", OS: "ubuntu", Backend: "cloudinit"}, osName: "ubuntu", backend: "cloudinit"}}
	m.form.imgIdx = 0
	m.form.inputs[fName].SetValue("box")

	out, _ := m.Update(keyMsg("enter"))
	after := out.(model)
	if after.form.fetching {
		t.Error("enter started a download; space is what downloads now")
	}
	if !strings.Contains(after.form.err, "space") {
		t.Errorf("form.err = %q, want it to point at space", after.form.err)
	}
}

// TestQuestionMarkTypesIntoTextFields covers the reported bug where "?" was
// always eaten as the help toggle, so it could never be typed into a text
// field. On a picker row it must still toggle help.
func TestQuestionMarkTypesIntoTextFields(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	key := keyMsg("?")

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fName
	m.form.refocus()
	got, _ := m.Update(key)
	after := got.(model)
	if after.showHelp {
		t.Error("? on a text field toggled help instead of typing")
	}
	if v := after.form.inputs[fName].Value(); v != "?" {
		t.Errorf("name field = %q, want %q", v, "?")
	}

	m2 := model{screen: screenForm, form: newForm()}
	m2.form.focus = fISO
	got2, _ := m2.Update(key)
	if !got2.(model).showHelp {
		t.Error("? on the image picker did not toggle help")
	}
}

// TestEscCancelsInFlightDownload checks that esc cancels the fetch
// goroutine, not just the UI's fetching flag. core.DownloadImage builds its
// request from the ctx fetchImage hands it, so cancelling that ctx (what esc
// does) stops the transfer itself.
func TestEscCancelsInFlightDownload(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	space := keyMsg(keySpace)
	m := model{screen: screenForm, form: newForm(), provisioning: map[string]provState{}}
	m.form.focus = fISO

	out, cmd := m.Update(space)
	m = out.(model)
	if !m.form.fetching || cmd == nil {
		t.Fatal("space did not start a download")
	}
	ctx := m.form.dlCtx
	if ctx == nil || ctx.Err() != nil {
		t.Fatal("download's ctx missing or already cancelled before esc")
	}

	out, _ = m.Update(keyMsg("esc"))
	m = out.(model)
	if m.screen != screenList {
		t.Fatalf("esc left screen %v, want the list", m.screen)
	}
	if m.form.fetching {
		t.Error("esc left fetching set even though the download was cancelled")
	}
	if m.form.dlCancel != nil {
		t.Error("esc left a stale cancel func behind")
	}
	if ctx.Err() == nil {
		t.Error("esc did not cancel the download's ctx: the goroutine is still reading")
	}

	// "n" is now safe to hand out a fresh form: nothing is writing anymore.
	before := m.form.inputs[fName].Value()
	m.form.inputs[fName].SetValue("marker")
	out, _ = m.Update(keyMsg("n"))
	m = out.(model)
	if m.screen != screenForm {
		t.Fatal(`"n" did not reopen the form`)
	}
	if m.form.inputs[fName].Value() == "marker" {
		t.Error(`"n" reused the cancelled form instead of handing out a fresh one`)
	}
	_ = before

	// A second space must start a genuinely new download rather than being
	// refused as if one were still in flight.
	m.form.focus = fISO
	out, cmd = m.Update(space)
	m = out.(model)
	if !m.form.fetching || cmd == nil {
		t.Fatal("a second download did not start after the first was cancelled")
	}
	if m.form.dlCancel == nil {
		t.Fatal("the second download has no cancel func")
	}
}

// TestStaleFetchOutcomeAfterCancelIsIgnored checks that a fetch outcome
// arriving after esc does not resurrect "fetching" or toast a "context
// canceled" the user did not cause. The goroutine core.DownloadImage runs in
// does not stop the instant ctx is cancelled; unblocking a Read takes one
// more pass, so its outcome message can still land after esc. The
// generation stamped on the message tells updateForm it is stale.
func TestStaleFetchOutcomeAfterCancelIsIgnored(t *testing.T) {
	m := model{screen: screenForm, form: newForm(), provisioning: map[string]provState{}}
	m.form.fetching = true
	m.form.dlGen = 1
	_, cancel := context.WithCancel(context.Background())
	m.form.dlCancel = cancel

	out, _ := m.Update(keyMsg("esc"))
	m = out.(model)
	if m.form.fetching {
		t.Fatal("esc should have cleared fetching immediately")
	}
	if m.form.dlGen != 2 {
		t.Fatalf("dlGen = %d, want 2 (esc must bump it so a stale message is recognisable)", m.form.dlGen)
	}

	// The abandoned goroutine's error, stamped with the OLD generation.
	out, cmd := m.Update(imageFetchErrMsg{entryID: "x", err: "context canceled", gen: 1})
	m = out.(model)
	if m.form.fetching {
		t.Error("a stale fetch outcome turned fetching back on")
	}
	if cmd != nil {
		t.Error("a stale fetch outcome should not toast: the user already cancelled it")
	}
}

// TestFetchOutcomesReachTheUserFromTheList checks that a download failing
// after the user has gone back to the list still reports itself, rather
// than dropping imageFetchErrMsg because it is routed by screen.
func TestFetchOutcomesReachTheUserFromTheList(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenList, form: newForm(), provisioning: map[string]provState{}}
	m.form.fetching = true
	m.form.fetchingOS = "ubuntu"

	out, _ := m.Update(imageFetchErrMsg{entryID: "ubuntu-24.04", err: "checksum mismatch", gen: m.form.dlGen})
	m = out.(model)
	if m.form.fetching {
		t.Error("a failed fetch left the fetching flag set")
	}
	if !strings.Contains(m.toast.text, "checksum mismatch") {
		t.Errorf("toast = %q, want the failure reported", m.toast.text)
	}
}

// alpine-cloud is a direct-URL catalog entry with Flavor == "" (see
// iso.Resolve's doc comment), so matchLocalImage must find its downloaded
// file by the same exact-basename match every other direct-URL entry uses.
// A gate of `e.OS != "alpine"` skips that basename match for every alpine
// entry, including alpine-cloud, and falls through to iso.Infer, which
// returns an empty OS for a .qcow2 filename. That empty OS never matches
// the catalog entry back, so the form would offer the download as an
// unlabeled BYO file forever.
func TestMatchLocalImageFindsDownloadedAlpineCloudFile(t *testing.T) {
	var entry iso.Entry
	for _, e := range iso.Catalog() {
		if e.ID == "alpine-cloud" {
			entry = e
			break
		}
	}
	if entry.ID == "" {
		t.Fatal("iso.Catalog() has no alpine-cloud entry")
	}

	u, err := url.Parse(entry.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := path.Base(u.Path)

	got := matchLocalImage(entry, []string{base})
	if got != base {
		t.Errorf("matchLocalImage(alpine-cloud, [%q]) = %q, want %q", base, got, base)
	}
}

func TestByoOptionFromPathMatchesLocalDiscovery(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "alpine-virt-3.20.0-x86_64.iso")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	opt, err := byoOptionFromPath(p)
	if err != nil {
		t.Fatalf("byoOptionFromPath: %v", err)
	}
	if !opt.isBYO() {
		t.Error("a browsed path must be marked BYO (entry == nil)")
	}
	if opt.file != p {
		t.Errorf("file = %q, want the absolute path %q", opt.file, p)
	}
	if opt.osName == "" {
		t.Error("iso.Infer should have named the OS for an alpine filename")
	}
}

// A path outside isos/ is the entire point of the feature.
func TestByoOptionFromPathAcceptsAnyDirectory(t *testing.T) {
	dir := t.TempDir() // deliberately NOT the stoat isos dir
	p := filepath.Join(dir, "custom.qcow2")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := byoOptionFromPath(p); err != nil {
		t.Fatalf("a path outside isos/ must be accepted: %v", err)
	}
}

// filepath.Join does not special-case an absolute second element, so joining
// isos/ onto a browsed path would give "…/isos/home/u/custom.iso". imagePath
// is what keeps the two cases apart.
func TestImagePathLeavesABrowsedAbsolutePathAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom.qcow2")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, err := byoOptionFromPath(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := opt.imagePath()
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("imagePath() = %q, want the browsed path %q", got, p)
	}
}

// A bare name still resolves under isos/, unchanged.
func TestImagePathResolvesABareNameUnderIsos(t *testing.T) {
	opt := imageOption{file: "alpine.iso"}
	got, err := opt.imagePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(config.Root(), "isos", "alpine.iso")
	if got != want {
		t.Errorf("imagePath() = %q, want %q", got, want)
	}
}

func TestByoOptionFromPathRejectsMissingFile(t *testing.T) {
	if _, err := byoOptionFromPath(filepath.Join(t.TempDir(), "nope.iso")); err == nil {
		t.Error("want an error for a path that does not exist")
	}
}

// b6593b3: a BYO image in cloudinit mode must resolve to cloudinit.User, not
// root. Cloud images lock root. A browsed path takes a different route into
// imageOption than localImageFiles, so it needs its own guard.
func TestBrowsedByoCloudinitResolvesSSHUser(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom.qcow2")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, err := byoOptionFromPath(p)
	if err != nil {
		t.Fatal(err)
	}
	f := formModel{images: []imageOption{opt}}
	if got := f.resolvedSSHUser(); got != cloudinit.User {
		t.Errorf("resolvedSSHUser = %q, want %q (root is locked on cloud images)", got, cloudinit.User)
	}
}
