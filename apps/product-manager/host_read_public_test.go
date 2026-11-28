//go:build !airlock_private

package main

import (
	"testing"

	"agent/internal/sourcewatch"
)

// The published SDK v0.11.0 cannot read a bound app tool from a job delivery.
// This build must say so instead of reporting a source it never read.
func TestPublicBuildRefusesBoundToolReads(t *testing.T) {
	if sourcewatch.HostSupportsAppRead {
		t.Fatal("the public build has no bound tool reader")
	}
}
