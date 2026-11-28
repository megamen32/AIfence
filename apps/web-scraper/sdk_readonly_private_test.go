//go:build airlock_private

package main

import "testing"

// The private platform SDK publishes a read-only bit in the tool manifest.
// The public SDK v0.11.0 has no such field, so this contract is asserted
// where the platform actually enforces it.
func TestScraperStatusReadOnlyManifest(t *testing.T) {
	for _, tool := range newAgent().Manifest().Tools {
		if tool.Name == "get_scraper_status" && !tool.ReadOnly {
			t.Fatal("scraper status must be declared read-only for source-watch bindings")
		}
	}
}
