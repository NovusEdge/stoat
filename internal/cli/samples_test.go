package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/project"
)

// The documented sample must decode in Reject mode, so a field renamed in
// project's structs cannot leave the docs describing a file stoat refuses.
func TestSampleProjectFileDecodes(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "samples", "stoat.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"src", "docs"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatalf("the documented sample does not load: %v", err)
	}
	if len(p.VMs) == 0 {
		t.Fatal("the sample declares no VM")
	}
	if _, err := p.Shares(p.VMs[0].Key); err != nil {
		t.Errorf("the sample's shares do not resolve: %v", err)
	}

	spec, err := core.SpecFor(p, p.VMs[0].Key)
	if err != nil {
		t.Fatalf("core.SpecFor cannot build a spec from the sample: %v", err)
	}
	for _, e := range iso.Catalog() {
		if e.ID == spec.Image {
			return
		}
	}
	t.Errorf("image %q in the sample is not a catalog id", spec.Image)
}
