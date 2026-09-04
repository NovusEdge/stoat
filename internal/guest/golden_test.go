package guest

import (
	"reflect"
	"testing"
)

// Guards the registry-to-file replacement: every bundled file must
// reproduce its literal entry before the literal is deleted. Deleted once
// the literal is gone, since there is nothing left to compare against.
func TestBundledMatchesLiteral(t *testing.T) {
	files := loadBundled()
	for _, lit := range registry {
		f, ok := files[lit.Name]
		if !ok {
			t.Fatalf("no file for %s", lit.Name)
		}
		type proj struct {
			Name, Shell, Installer, Backend, SSHUser, Install string
			Init                                              InitSystem
			Seed, Hints                                       []string
		}
		want := proj{lit.Name, lit.Shell, lit.Installer, lit.Backend, lit.DefaultSSHUser, lit.PkgInstall, lit.Init, lit.SeedPackages, lit.FilenameHints}
		got := proj{f.Name, f.Shell, f.Installer, f.DefaultBackend, f.DefaultSSHUser, f.Pkg.ScaffoldInstall, f.Init, f.SeedPackages, f.FilenameHints}
		if want.Seed == nil {
			want.Seed = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", lit.Name, got, want)
		}
	}
}
