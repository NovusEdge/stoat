package cloudinit

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// mergeDocs folds every cloud-config document into one mapping and renders it
// as a single "#cloud-config" document.
//
// stoat used to hand cloud-init a cloud-config-archive and let it merge the
// documents. An archive is a top-level YAML list, and cloud-init 24.4 on
// AlmaLinux 9 and Rocky 9 reads user-data with .get() before it checks the
// type, so the list fails the init-local stage and pins `cloud-init status` at
// error for the life of the VM. Merging here removes the dependency on the
// guest's cloud-init version.
//
// The rules match the merge_how stoat used to declare,
// "list(append)+dict(recurse_list)": a list appends to the list already there,
// a mapping merges key by key, and anything else takes the later value. Every
// document stoat produces or reads is a cloud-config mapping, so a document
// that parses as anything else is a caller error and fails the seed.
func mergeDocs(docs []string) (string, error) {
	merged := map[string]any{}
	for i, doc := range docs {
		var parsed map[string]any
		body := strings.TrimPrefix(doc, "#cloud-config\n")
		if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
			return "", fmt.Errorf("cloud-config document %d: %w", i+1, err)
		}
		if parsed == nil {
			continue
		}
		merged = mergeMaps(merged, parsed)
	}
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("marshaling cloud-config: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

func mergeMaps(into, from map[string]any) map[string]any {
	for k, v := range from {
		existing, ok := into[k]
		if !ok {
			into[k] = v
			continue
		}
		switch old := existing.(type) {
		case map[string]any:
			if next, ok := v.(map[string]any); ok {
				into[k] = mergeMaps(old, next)
				continue
			}
		case []any:
			if next, ok := v.([]any); ok {
				into[k] = append(old, next...)
				continue
			}
		}
		into[k] = v
	}
	return into
}
