//go:build airlock_private

package main

import "testing"

// The private platform SDK publishes a read-only bit in the tool manifest.
// The public SDK v0.11.0 has no such field, so this contract is asserted
// where the platform actually enforces it.
func TestWeeklyPriceToolsReadOnlyManifest(t *testing.T) {
	want := map[string]bool{"price_week_delta": true, "family_price_week_delta": true}
	for _, definition := range newAgent().Manifest().Tools {
		if !want[definition.Name] {
			continue
		}
		if !definition.ReadOnly {
			t.Fatalf("cross-app source %s must be read-only", definition.Name)
		}
		delete(want, definition.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing cross-app sources: %v", want)
	}
}
