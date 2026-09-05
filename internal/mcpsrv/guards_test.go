package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckVMName(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		for _, n := range []string{"work", "Work", "w", "work-1", "work_1", "work.1", "1work", "a.b-c_d"} {
			if got, err := checkVMName(n); err != nil || got != n {
				t.Errorf("checkVMName(%q) = %q, %v; want %q, nil", n, got, err, n)
			}
		}
	})
	t.Run("rejects", func(t *testing.T) {
		for _, n := range []string{"", " ", "work ", " work", ".", "..", ".hidden", "-lead", "work/evil", "work\x00", "wörk!"} {
			if _, err := checkVMName(n); err == nil {
				t.Errorf("checkVMName(%q) accepted", n)
			}
		}
	})
	t.Run("absolute_path", func(t *testing.T) {
		if _, err := checkVMName("/etc/passwd"); err == nil {
			t.Fatal("accepted an absolute path")
		}
	})
	t.Run("unicode_separator", func(t *testing.T) {
		// U+2044 FRACTION SLASH is not a path separator, so the separator
		// check misses it and the name pattern is what refuses it.
		if _, err := checkVMName("work⁄evil"); err == nil {
			t.Fatal("accepted a unicode lookalike separator")
		}
	})
}

func TestCheckImageID(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		for _, s := range []string{"alpine-virt", "ubuntu-24.04", "debian_12"} {
			if _, err := checkImageID(s); err != nil {
				t.Errorf("checkImageID(%q): %v", s, err)
			}
		}
	})
	t.Run("rejects_paths", func(t *testing.T) {
		for _, s := range []string{"", "/abs/x.qcow2", "./x.qcow2", "~/x.qcow2", "a/b", "../x"} {
			if _, err := checkImageID(s); err == nil {
				t.Errorf("checkImageID(%q) accepted", s)
			}
		}
	})
}

func TestCheckHostPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	sandbox := filepath.Join(root, "shared", "work")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(sandbox, "f.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("inside_sandbox", func(t *testing.T) {
		got, err := checkHostPath(inside, "work")
		if err != nil || got != inside {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("sandbox_root", func(t *testing.T) {
		if _, err := checkHostPath(sandbox, "work"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("relative", func(t *testing.T) {
		if _, err := checkHostPath("f.txt", "work"); err == nil {
			t.Fatal("accepted a relative path")
		}
	})
	t.Run("traversal", func(t *testing.T) {
		if _, err := checkHostPath(filepath.Join(sandbox, "..", "..", "id_stoat"), "work"); err == nil {
			t.Fatal("accepted a traversal")
		}
	})
	t.Run("outside", func(t *testing.T) {
		if _, err := checkHostPath("/etc/passwd", "work"); err == nil {
			t.Fatal("accepted /etc/passwd")
		}
	})
	t.Run("sibling_prefix", func(t *testing.T) {
		evil := filepath.Join(root, "shared", "work-evil")
		if err := os.MkdirAll(evil, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := checkHostPath(filepath.Join(evil, "f.txt"), "work"); err == nil {
			t.Fatal("accepted a sibling that shares a string prefix")
		}
	})
	t.Run("symlink_escape", func(t *testing.T) {
		link := filepath.Join(sandbox, "out")
		if err := os.Symlink(root, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		defer func() {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := checkHostPath(filepath.Join(link, "id_stoat"), "work"); err == nil {
			t.Fatal("accepted a path through an escaping symlink")
		}
	})
	t.Run("symlink_direct", func(t *testing.T) {
		link := filepath.Join(sandbox, "passwd")
		if err := os.Symlink("/etc/passwd", link); err != nil {
			t.Skip("symlinks unavailable")
		}
		defer func() {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := checkHostPath(link, "work"); err == nil {
			t.Fatal("accepted a symlink pointing out")
		}
	})
	t.Run("symlink_inside", func(t *testing.T) {
		link := filepath.Join(sandbox, "alias.txt")
		if err := os.Symlink(inside, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		defer func() {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
		}()
		got, err := checkHostPath(link, "work")
		if err != nil {
			t.Fatal(err)
		}
		if got != inside {
			t.Fatalf("got %q, want the resolved target %q", got, inside)
		}
	})
	t.Run("empty", func(t *testing.T) {
		for _, p := range []string{"", "   "} {
			if _, err := checkHostPath(p, "work"); err == nil {
				t.Errorf("accepted %q", p)
			}
		}
	})
	t.Run("null_byte", func(t *testing.T) {
		if _, err := checkHostPath(inside+"\x00", "work"); err == nil {
			t.Fatal("accepted a null byte")
		}
	})
	t.Run("tilde", func(t *testing.T) {
		t.Setenv("HOME", root)
		if _, err := checkHostPath("~/shared/work/f.txt", "work"); err != nil {
			t.Fatalf("~ did not expand to the caller's home: %v", err)
		}
	})
	t.Run("disjoint_sandboxes", func(t *testing.T) {
		if _, err := checkHostPath(inside, "other"); err == nil {
			t.Fatal("work's file was accepted for vm \"other\"")
		}
	})
	t.Run("bad_vm_name", func(t *testing.T) {
		if _, err := checkHostPath(inside, "../evil"); err == nil {
			t.Fatal("accepted a bad vm name as a path component")
		}
	})
}

func TestStripForbidden(t *testing.T) {
	t.Run("removes_every_key", func(t *testing.T) {
		in := map[string]any{"ram": 2048, "share": "/etc", "image": "/x.qcow2", "base": "b", "iso": "i", "console_password": "p"}
		got := stripForbidden(in)
		if len(got) != 1 || got["ram"] != 2048 {
			t.Fatalf("got %v, want only ram", got)
		}
	})
	t.Run("clean_patch", func(t *testing.T) {
		in := map[string]any{"ram": 2048, "cpus": 2}
		if got := stripForbidden(in); len(got) != 2 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("does_not_mutate", func(t *testing.T) {
		in := map[string]any{"ram": 2048, "share": "/etc"}
		stripForbidden(in)
		if _, ok := in["share"]; !ok {
			t.Fatal("stripForbidden mutated its input")
		}
	})
}

func TestCheckIndexName(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		for _, c := range []struct{ in, name, ref string }{
			{"tailscale", "tailscale", ""},
			{"tailscale@v1.2", "tailscale", "v1.2"},
			{"my-recipe@main", "my-recipe", "main"},
			{"tailscale@feature/topic", "tailscale", "feature/topic"},
		} {
			name, ref, err := checkIndexName(c.in)
			if err != nil || name != c.name || ref != c.ref {
				t.Errorf("checkIndexName(%q) = %q,%q,%v", c.in, name, ref, err)
			}
		}
	})
	t.Run("rejects", func(t *testing.T) {
		for _, s := range []string{
			"", "https://github.com/x/y", "git@github.com:x/y.git",
			"x/y", "../evil", "a@b@c", "tailscale@", "@v1", "Tailscale",
			"tail scale", "tailscale@../evil", "-y", "tailscale@feature..topic",
			"tailscale@feature/.hidden", "tailscale@feature/topic.lock",
			"tailscale@feature/",
		} {
			if _, _, err := checkIndexName(s); err == nil {
				t.Errorf("checkIndexName(%q) accepted", s)
			}
		}
	})
}

func TestCheckParamName(t *testing.T) {
	for _, s := range []string{"user", "auth_key", "u1"} {
		if _, err := checkParamName(s); err != nil {
			t.Errorf("checkParamName(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "User", "1user", "auth-key", "auth key", "_user"} {
		if _, err := checkParamName(s); err == nil {
			t.Errorf("checkParamName(%q) accepted", s)
		}
	}
}

func TestCheckGuestPath(t *testing.T) {
	for _, s := range []string{"/etc/hosts", "/var/log/messages", "/a b/c"} {
		if _, err := checkGuestPath(s); err != nil {
			t.Errorf("checkGuestPath(%q): %v", s, err)
		}
	}
	// A relative path is an error, never resolved against $HOME, so a tool
	// call means the same thing on every guest.
	for _, s := range []string{"", "etc/hosts", "./x", "~/x", "/x\x00"} {
		if _, err := checkGuestPath(s); err == nil {
			t.Errorf("checkGuestPath(%q) accepted", s)
		}
	}
}

func TestCheckSvcName(t *testing.T) {
	for _, s := range []string{"docker", "sshd", "getty@tty1", "my.service"} {
		if _, err := checkSvcName(s); err != nil {
			t.Errorf("checkSvcName(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "a b", "a;b", "$(x)", "a/b", "-x"} {
		if _, err := checkSvcName(s); err == nil {
			t.Errorf("checkSvcName(%q) accepted", s)
		}
	}
}

func TestCheckFlagFree(t *testing.T) {
	t.Run("long_flag", func(t *testing.T) {
		if err := checkFlagFree([]string{"--clear"}, "pairs"); err == nil {
			t.Fatal("accepted --clear")
		}
	})
	t.Run("short_flag_and_empty", func(t *testing.T) {
		for _, v := range []string{"-y", "", "  "} {
			if err := checkFlagFree([]string{v}, "pairs"); err == nil {
				t.Errorf("accepted %q", v)
			}
		}
	})
	t.Run("ordinary", func(t *testing.T) {
		if err := checkFlagFree([]string{"8080:80", "docker"}, "pairs"); err != nil {
			t.Fatal(err)
		}
	})
}
