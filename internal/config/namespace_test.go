package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeVM(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDirForKeepsAnExistingLegacyVMInPlace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "dev"), "name = \"dev\"\n")
	if got, want := DirFor("dev"), filepath.Join(root, "dev"); got != want {
		t.Errorf("DirFor = %q, want %q: an existing VM never moves", got, want)
	}
}

func TestDirForPlacesANewVMUnderV2(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if got, want := DirFor("dev"), filepath.Join(root, "v2", "dev"); got != want {
		t.Errorf("DirFor = %q, want %q", got, want)
	}
}

func TestListFindsBothNamespaces(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "old"), "name = \"old\"\n")
	writeVM(t, filepath.Join(root, "v2", "new"), "name = \"new\"\nprovider = \"gce\"\n")
	vms, err := List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("List() returned %d VMs, want 2", len(vms))
	}
	if vms[0].Name != "new" || vms[1].Name != "old" {
		t.Errorf("List() = %q, %q; want new, old sorted by name", vms[0].Name, vms[1].Name)
	}
}

func TestListNeverReportsV2AsAVM(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "v2", "new"), "name = \"new\"\nprovider = \"gce\"\n")
	vms, err := List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, v := range vms {
		if v.Name == "v2" {
			t.Error("List() returned the v2 directory itself as a VM")
		}
	}
}

func TestExistsSpansBothNamespaces(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "v2", "dev"), "name = \"dev\"\nprovider = \"gce\"\n")
	if !Exists("dev") {
		t.Error("Exists(dev) = false; a v2 name is taken and must not be reused")
	}
}

func TestSaveSendsAProviderVMToV2(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &VM{Name: "cloud", Provider: "gce", SSHPort: 2222}
	if err := v.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "v2", "cloud", "vm.toml")); err != nil {
		t.Errorf("v2/cloud/vm.toml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cloud", "vm.toml")); err == nil {
		t.Error("a provider VM must not be written where a legacy binary can read it")
	}
}

func TestSaveKeepsAQemuVMFlat(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &VM{Name: "local", SSHPort: 2223}
	if err := v.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "local", "vm.toml")); err != nil {
		t.Errorf("local/vm.toml missing: %v", err)
	}
}

func TestProviderOfReadsAnUnparseableV2Record(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "v2", "cloud"), "name = \"cloud\nprovider = \"gce\"\n")
	got, err := ProviderOf("cloud")
	if err != nil {
		t.Fatalf("ProviderOf() error = %v", err)
	}
	if got == "qemu" || got == "" {
		t.Errorf("ProviderOf = %q; a broken v2 record must not resolve to the local hypervisor", got)
	}
}

func TestProviderOfReadsALegacyRecordAsQemu(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, filepath.Join(root, "old"), "name = \"old\nmode = \"live\n")
	got, err := ProviderOf("old")
	if err != nil {
		t.Fatalf("ProviderOf() error = %v", err)
	}
	if got != "qemu" {
		t.Errorf("ProviderOf = %q, want qemu for a flat record", got)
	}
}
