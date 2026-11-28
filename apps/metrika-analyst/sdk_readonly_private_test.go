//go:build airlock_private

package main

import "testing"

// The private platform SDK publishes a read-only bit in the tool manifest.
// The public SDK v0.11.0 has no such field, so this contract is asserted
// where the platform actually enforces it.
func TestSourceToolsReadOnlyManifest(t *testing.T) {
	want := map[string]bool{"compare_periods": true, "compare_demand": true, "compare_wordstat": true, "compare_google_trends": true}
	for _, definition := range newAgent().Manifest().Tools {
		if !want[definition.Name] {
			continue
		}
		readOnly, supported := manifestReadOnly(definition)
		if !supported {
			t.Fatalf("private SDK must publish the read-only bit for %s", definition.Name)
		}
		if !readOnly {
			t.Fatalf("source tool %s must be read-only", definition.Name)
		}
		delete(want, definition.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing read-only source tools: %v", want)
	}
}
