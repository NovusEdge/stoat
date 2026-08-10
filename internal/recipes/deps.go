package recipes

import (
	"fmt"
	"strings"
)

// TopoSort orders names so each recipe follows every recipe it depends on.
// manifests maps each name to its Manifest. A Depends edge to a name absent
// from manifests is ignored: that dependency is outside this set, and the
// caller checks it for "already applied" separately. A dependency cycle
// returns an error naming the path, e.g.
// "cycle detected: devtools -> docker -> devtools".
//
// The sort is a depth-first post-order over names in the given order, so the
// result is deterministic for a given input order.
func TopoSort(names []string, manifests map[string]Manifest) ([]string, error) {
	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(names))
	var order, stack []string

	var visit func(string) error
	visit = func(name string) error {
		color[name] = gray
		stack = append(stack, name)
		for _, dep := range manifests[name].Depends {
			if _, ok := manifests[dep]; !ok {
				continue // dependency outside this set; not ordered here
			}
			switch color[dep] {
			case gray:
				return cycleError(stack, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		order = append(order, name)
		return nil
	}

	for _, name := range names {
		if color[name] == white {
			if err := visit(name); err != nil {
				return nil, err
			}
		}
	}
	return order, nil
}

// cycleError builds the "a -> b -> a" message from the DFS stack and the gray
// node the back edge points to. back appears once in stack; the slice from
// there to the end, plus back again, is the cycle.
func cycleError(stack []string, back string) error {
	start := 0
	for i, n := range stack {
		if n == back {
			start = i
			break
		}
	}
	cycle := append(append([]string{}, stack[start:]...), back)
	return fmt.Errorf("cycle detected: %s", strings.Join(cycle, " -> "))
}
