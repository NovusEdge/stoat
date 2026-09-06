package cloudinit

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseMapping(t *testing.T, doc string) map[string]any {
	t.Helper()
	const header = "#cloud-config\n"
	if !strings.HasPrefix(doc, header) {
		t.Fatalf("document does not start with %q:\n%s", header, doc)
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("document is not a mapping: %v\n%s", err, doc)
	}
	return out
}

func TestMergeDocsAppendsLists(t *testing.T) {
	got, err := mergeDocs([]string{
		"#cloud-config\npackages:\n  - sudo\n",
		"#cloud-config\npackages:\n  - git\n  - tmux\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	packages, ok := parseMapping(t, got)["packages"].([]any)
	if !ok {
		t.Fatalf("packages is not a list:\n%s", got)
	}
	want := []string{"sudo", "git", "tmux"}
	if len(packages) != len(want) {
		t.Fatalf("packages = %v, want %v", packages, want)
	}
	for i, w := range want {
		if packages[i] != w {
			t.Errorf("packages[%d] = %v, want %q", i, packages[i], w)
		}
	}
}

func TestMergeDocsKeepsListOrderAcrossKeys(t *testing.T) {
	got, err := mergeDocs([]string{
		"#cloud-config\nruncmd:\n  - first\n",
		"#cloud-config\nruncmd:\n  - second\n",
		"#cloud-config\nruncmd:\n  - third\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	runcmd := parseMapping(t, got)["runcmd"].([]any)
	for i, want := range []string{"first", "second", "third"} {
		if runcmd[i] != want {
			t.Errorf("runcmd[%d] = %v, want %q", i, runcmd[i], want)
		}
	}
}

func TestMergeDocsRecursesIntoMappings(t *testing.T) {
	got, err := mergeDocs([]string{
		"#cloud-config\napt:\n  sources:\n    one:\n      key: a\n",
		"#cloud-config\napt:\n  sources:\n    two:\n      key: b\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	apt := parseMapping(t, got)["apt"].(map[string]any)
	sources := apt["sources"].(map[string]any)
	if _, ok := sources["one"]; !ok {
		t.Errorf("the first document's nested key was dropped:\n%s", got)
	}
	if _, ok := sources["two"]; !ok {
		t.Errorf("the second document's nested key was dropped:\n%s", got)
	}
}

// A later scalar wins. cloud-init's own dict merge behaves this way, and the
// callers rely on it: a recipe that sets a scalar means to set it.
func TestMergeDocsLaterScalarWins(t *testing.T) {
	got, err := mergeDocs([]string{
		"#cloud-config\nssh_pwauth: false\n",
		"#cloud-config\nssh_pwauth: true\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parseMapping(t, got)["ssh_pwauth"] != true {
		t.Errorf("the later scalar lost:\n%s", got)
	}
}

func TestMergeDocsKeepsWriteFiles(t *testing.T) {
	got, err := mergeDocs([]string{
		"#cloud-config\nwrite_files:\n  - path: /etc/one\n    content: a\n",
		"#cloud-config\nwrite_files:\n  - path: /etc/two\n    content: b\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	files := parseMapping(t, got)["write_files"].([]any)
	if len(files) != 2 {
		t.Fatalf("write_files has %d entries, want 2:\n%s", len(files), got)
	}
}

func TestMergeDocsRejectsANonMapping(t *testing.T) {
	if _, err := mergeDocs([]string{"#cloud-config\n- one\n- two\n"}); err == nil {
		t.Error("mergeDocs accepted a document that is a list")
	}
}

func TestMergeDocsReportsTheBadDocument(t *testing.T) {
	_, err := mergeDocs([]string{
		"#cloud-config\npackages:\n  - sudo\n",
		"#cloud-config\npackages:\n  - : :\n",
	})
	if err == nil {
		t.Fatal("mergeDocs accepted invalid YAML")
	}
	if !strings.Contains(err.Error(), "document 2") {
		t.Errorf("error does not name the failing document: %v", err)
	}
}
