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
// .part files are excluded. iso.Infer matches one anyway
// ("…cloudimg-amd64.img.part" contains "cloudimg"), so an included .part
// file would show up as selectable and let a VM build on a truncated
// image. Downloads run minutes with no cancel key, so aborted ones, and
// their .part files, are common.
func LocalImages() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(config.Root(), "isos"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".part") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// MatchLocal reports which local file, if any, satisfies catalog entry e.
// A direct-URL entry matches the exact basename of e.URL. An entry
// resolved through an index instead of a fixed filename matches whatever
// local file iso.Infer assigns the same OS/backend pair.
//
// The discriminator is e.Flavor, not e.OS == "alpine". alpine-cloud is an
// alpine entry with a direct URL and Flavor == "", like any other
// direct-URL entry (see iso.Resolve's doc comment). Gating on OS alone
// would skip the basename match for every alpine entry and fall through to
// the Infer loop, which can match the wrong file when several share a
// backend/OS pair.
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
		// A flavoured entry must also match its flavour in the filename.
		//
		// iso.Infer reports only an OS and a backend. alpine-standard and
		// alpine-virt share both, so without this check the first alpine
		// .iso on disk satisfied both entries. `stoat images` then
		// reported alpine-virt as downloaded when only the standard ISO
		// was present, and creating from alpine-virt silently built a VM
		// on the standard image: a 352 MiB general-purpose kernel instead
		// of the 66 MiB virtualised one the user asked for.
		//
		// Alpine's published filenames begin with the catalog's flavour
		// string ("alpine-virt" -> alpine-virt-3.24.1-x86_64.iso), which
		// makes this a reliable check. Entries with no flavour already
		// matched by exact basename above.
		if e.Flavor != "" && !strings.Contains(f, e.Flavor) {
			continue
		}
		return f
	}
	return ""
}

// image is everything Create needs to know about the file a Spec named.
type image struct {
	abs     string
	rel     string     // bare name under isos/; "" when abs lives elsewhere
	entry   *iso.Entry // nil for BYO
	osName  string
	backend string
	sshUser string
}

// isoField is what vm.toml records. A bare name stays relative to the data
// root, so the VM survives a moved $STOAT_HOME. A file browsed to outside
// isos/ must stay absolute; joining "isos/" onto it would produce
// "…/isos/home/u/x.iso", a path that does not exist.
func (i image) isoField() string {
	if i.rel != "" {
		return "isos/" + i.rel
	}
	return i.abs
}

// id names the image for a drift comparison: the catalog id when the image
// came from the catalog, the absolute path otherwise. A BYO image has no
// catalog id, so the path is the only stable identifier to compare against a
// re-declared vms.<key>.image.
func (i image) id() string {
	if i.entry != nil {
		return i.entry.ID
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

	files, err := LocalImages()
	if err != nil {
		return image{}, err
	}
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

// apply folds a Spec's BYO overrides onto what the file itself declared,
// and derives the SSH user from the result.
//
// The cloud-init seed creates exactly one account, cloudinit.User.
// Anything provisioned through that backend must connect as it, including
// a file the caller has just declared to be a cloud image. Without this, a
// BYO image would record an empty SSHUser, sshx would default that to
// root, and cloud images lock root: ssh and recipes would both fail on a
// VM that looked correctly configured.
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
