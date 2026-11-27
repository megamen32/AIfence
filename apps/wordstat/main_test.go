package main

import (
	"encoding/json"
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
}

func TestToolDataSchemaIsObject(t *testing.T) {
	for _, tool := range newAgent().Manifest().Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: missing output properties", tool.Name)
		}
		data, ok := properties["data"].(map[string]any)
		if !ok || data["type"] != "object" {
			t.Fatalf("%s: data must accept a JSON object, got %v", tool.Name, properties["data"])
		}
	}
}
