package main

import (
	"slices"
	"testing"

	"github.com/airlockrun/agentsdk/wire"
)

func TestNativeSourceWatchManifest(t *testing.T) {
	m := newAgent().Manifest()
	if !slices.ContainsFunc(m.JobHandlers, func(h wire.JobHandlerDef) bool { return h.Name == "check_source_watch" && h.Version == 1 }) {
		t.Fatal("missing native source observation job")
	}
	if !slices.ContainsFunc(m.AgentDefinitions, func(d wire.AgentDefinition) bool {
		return d.Slug == "review_source_signal" && d.Review != nil && d.Review.MaxCorrections == 2 && d.Capabilities != nil && len(d.Capabilities.Allow) == 0
	}) {
		t.Fatal("source review must validate evidence before publishing a result")
	}
	for _, name := range []string{"start_source_watch", "stop_source_watch", "list_source_watches", "review_source_watch"} {
		if !slices.ContainsFunc(m.Tools, func(t wire.ToolDef) bool { return t.Name == name && t.Access == wire.AccessUser }) {
			t.Errorf("missing owner tool %s", name)
		}
	}
	if len(m.JobCrons) != 0 {
		t.Fatal("owner-scoped reads must retain the initiating user's authority")
	}
}
