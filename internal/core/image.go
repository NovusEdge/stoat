package core

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

// LocalImages lists every plain file under isos/, any extension, so BYO
// qcow2/img cloud images are picked up alongside ISOs.
//
// .part files are half-finished downloads. iso.Infer happily matches one
// ("…cloudimg-amd64.img.part" contains "cloudimg"), so without this they show
// up as selectable images and a VM can be built on a truncated file. Aborting
// a download is routine, minutes long with no cancel key, so these do
// accumulate.
func LocalImages() []string {
	entries, err := os.ReadDir(filepath.Join(config.Root(), "isos"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".part") {
			out = append(out, e.Name())
		}
	}
	return out
}

// MatchLocal reports which local file (if any) satisfies catalog entry e:
// either the exact basename of e.URL (direct-URL entries), or, for entries
// resolved through an index rather than a fixed filename, whatever local file
// iso.Infer agrees belongs to e's OS/backend pair.
//
// The discriminator is e.Flavor, not e.OS == "alpine": alpine-cloud is an
// alpine entry with a direct URL and Flavor == "", same as any other
// direct-URL entry (see iso.Resolve's doc comment for why). Gating on OS alone
// skipped the exact basename match for every alpine entry, including this one,
// and fell through to the Infer-based loop below, which, depending on the
// local directory's contents, can match the wrong file among several with the
// same backend/OS pair.
func MatchLocal(e iso.Entry, files []string) string {
	if e.Flavor == "" && e.URL != "" {
		if u, err := url.Parse(e.URL); err == nil {
			base := path.Base(u.Path)
			for _, f := range files {
				if f == base {
					return f
				}
			}
		}
	}
	for _, f := range files {
		backend, osName := iso.Infer(f)
		if backend != e.Backend || osName != e.OS {
			continue
		}
		// A FLAVOURED entry must also match its flavour in the filename.
		//
		// iso.Infer only reports an OS and a backend, and alpine-standard and
		// alpine-virt share both, so without this the first alpine .iso on
		// disk satisfied BOTH entries. `stoat images` then reported alpine-virt
		// as downloaded when only the standard ISO was present, and creating
		// from alpine-virt silently built a VM on the standard image: a 352 MiB
		// general-purpose kernel where the user asked for the 66 MiB
		// virtualised one. Wrong image, no error, no way to notice.
		//
		// Alpine's published filenames begin with exactly the flavour string
		// the catalog stores ("alpine-virt" -> alpine-virt-3.24.1-x86_64.iso),
		// which is what makes this a reliable discriminator rather than a
		// guess. Entries with no flavour are unaffected: they matched by exact
		// basename above.
		if e.Flavor != "" && !strings.Contains(f, e.Flavor) {
			continue
		}
		return f
	}
	return ""
}

// image is everything Create needs to know about the file a Spec named.
type image struct {
	abs     string     // absolute path to the file on disk
	rel     string     // bare name under isos/; "" when abs lives elsewhere
	entry   *iso.Entry // nil for BYO
	osName  string
	backend string
	sshUser string
}

// isoField is what vm.toml records. A bare name stays relative to the data
// root so the VM survives a moved $STOAT_HOME; a file browsed to outside
// isos/ has to be absolute, and joining "isos/" onto it would produce
// "…/isos/home/u/x.iso", a path that does not exist.
func (i image) isoField() string {
	if i.rel != "" {
		return "isos/" + i.rel
	}
	return i.abs
}

// resolveImage turns Spec.Image into a concrete file. It accepts a catalog
// entry ID, a bare filename under isos/, or an absolute path to anything.
//
// A catalog entry that has not been downloaded is ErrImageNotDownloaded and
// not an inference failure: the caller can fix it by fetching the image.
func resolveImage(spec string) (image, error) {
	if spec == "" {
		return image{}, fmt.Errorf("%w: no image given", ErrNotFound)
	}
	if filepath.IsAbs(spec) {
		st, err := os.Stat(spec)
		if err != nil {
			return image{}, err
		}
		if st.IsDir() {
			return image{}, fmt.Errorf("%s is a directory", spec)
		}
		backend, osName := iso.Infer(filepath.Base(spec))
		return image{abs: spec, backend: backend, osName: osName}, nil
	}

	files := LocalImages()
	for _, e := range iso.Catalog() {
		if e.ID != spec {
			continue
		}
		f := MatchLocal(e, files)
		if f == "" {
			return image{}, fmt.Errorf("%w: %s", ErrImageNotDownloaded, e.ID)
		}
		abs, err := filepath.Abs(filepath.Join(config.Root(), "isos", f))
		if err != nil {
			return image{}, err
		}
		return image{abs: abs, rel: f, entry: &e, osName: e.OS, backend: e.Backend, sshUser: e.SSHUser}, nil
	}

	for _, f := range files {
		if f != spec {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(config.Root(), "isos", f))
		if err != nil {
			return image{}, err
		}
		backend, osName := iso.Infer(f)
		return image{abs: abs, rel: f, backend: backend, osName: osName}, nil
	}
	return image{}, fmt.Errorf("%w: no catalog entry or local image called %q", ErrNotFound, spec)
}

// apply folds a Spec's BYO overrides onto what the file itself declared, and
// derives the SSH user from the result.
//
// The cloud-init seed creates exactly one account, cloudinit.User, so anything
// provisioned through that backend connects as it, including a file the
// caller has just declared to be a cloud image. Left to fall through, a BYO
// image would record an empty SSHUser, sshx would default that to root, and
// cloud images lock root: ssh and applying recipes would both fail on a VM
// that looked correctly configured.
func (i image) apply(s Spec) image {
	if i.entry == nil {
		if s.Backend != "" {
			i.backend = s.Backend
		}
		if s.OS != "" {
			i.osName = s.OS
		}
	}
	if i.backend == "cloudinit" {
		i.sshUser = cloudinit.User
	}
	return i
}
