//go:build airlock_private

package main

import (
	"testing"

	"agent/internal/sourcewatch"
)

// The private platform SDK exposes JobContext.ReadAppTool, so the weekly
// source watch must use owner-scoped job reads. The published SDK v0.11.0 has
// no such reader and is covered by host_read_public_test.go instead.
func TestPrivateBuildUsesOwnerScopedJobReads(t *testing.T) {
	if !sourcewatch.HostSupportsAppRead {
		t.Fatal("native source watch must use owner-scoped job reads")
	}
}
