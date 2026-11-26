package main

import (
	"slices"
	"testing"

	"github.com/airlockrun/agentsdk/wire"
)

// TestAgentComposition checks the offline definition without starting a runtime.
// HTTP admission, migrations, sync and OnStart execute on real Airlock previews.
func TestAgentComposition(t *testing.T) {
	manifest := newAgent().Manifest()
	if manifest.Icon == "" {
		t.Fatal("app must declare an Icon emoji")
	}
	if err := wire.CheckAppRuntimeProtocol(manifest.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) == 0 {
		t.Fatal("app must declare its HTTP routes")
	}
	for _, name := range []string{"save_hypothesis", "investigate_orders", "send_to_backlog", "evaluate_hypothesis"} {
		if !slices.ContainsFunc(manifest.Tools, func(tool wire.ToolDef) bool {
			return tool.Name == name && tool.Access == wire.AccessUser && len(tool.InputSchema) > 0
		}) {
			t.Errorf("missing typed user tool %s", name)
		}
	}
	if !slices.ContainsFunc(manifest.Topics, func(topic wire.TopicDef) bool {
		return topic.Slug == "product_signals" && topic.PerUser && topic.Enrollment == wire.TopicEnrollmentDefaultOff
	}) {
		t.Fatal("missing opt-in signal topic")
	}
}
