package recipes

import (
	"strings"
	"testing"
)

func mans(deps map[string][]string) map[string]Manifest {
	m := make(map[string]Manifest, len(deps))
	for name, d := range deps {
		m[name] = Manifest{Name: name, Depends: d}
	}
	return m
}

// TestTopoSort pins that a dependency lands before its dependent regardless of
// input order.
func TestTopoSort(t *testing.T) {
	manifests := mans(map[string][]string{
		"devtools": {"docker"},
		"docker":   nil,
		"xfce":     nil,
	})
	order, err := TopoSort([]string{"devtools", "docker", "xfce"}, manifests)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["docker"] > pos["devtools"] {
		t.Errorf("order = %v, want docker before devtools", order)
	}
	if len(order) != 3 {
		t.Errorf("order = %v, want all three recipes", order)
	}
}

// TestTopoSortIgnoresOutsideDep pins that a Depends edge to a name not in the
// set is not an error: it is a dependency checked for "already applied"
// elsewhere, not ordered here.
func TestTopoSortIgnoresOutsideDep(t *testing.T) {
	manifests := mans(map[string][]string{"devtools": {"docker"}})
	order, err := TopoSort([]string{"devtools"}, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "devtools" {
		t.Errorf("order = %v, want [devtools]", order)
	}
}

// TestCycleDetection catches a direct A->B->A cycle and names the path.
func TestCycleDetection(t *testing.T) {
	manifests := mans(map[string][]string{
		"devtools": {"docker"},
		"docker":   {"devtools"},
	})
	_, err := TopoSort([]string{"devtools", "docker"}, manifests)
	if err == nil {
		t.Fatal("want a cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle detected") {
		t.Errorf("err = %q, want a \"cycle detected\" message", err)
	}
	// The path names both recipes and closes the loop.
	if !strings.Contains(err.Error(), "devtools") || !strings.Contains(err.Error(), "docker") {
		t.Errorf("err = %q, want it to name both recipes in the cycle", err)
	}
}

// TestCycleDetectionSelfLoop catches a recipe that depends on itself.
func TestCycleDetectionSelfLoop(t *testing.T) {
	manifests := mans(map[string][]string{"loop": {"loop"}})
	_, err := TopoSort([]string{"loop"}, manifests)
	if err == nil || !strings.Contains(err.Error(), "cycle detected") {
		t.Fatalf("err = %v, want a cycle error", err)
	}
}
