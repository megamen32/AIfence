package main

import (
	"agent/trends"
	"encoding/json"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/agentsdk/wire"
	"testing"
)

func TestOfflineManifestDeclaresPrivateTypedToolsAndHeaderAuth(t *testing.T) {
	m := newAgent().Manifest()
	if err := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(m.MCPServers) != 1 || len(m.Tools) != 3 || len(m.SuggestedPrompts) != 3 {
		t.Fatal("missing declarations")
	}
	server := m.MCPServers[0]
	if server.URL != trends.Endpoint || server.Slug != "hasdata_google_trends" || string(server.AuthMode) != string(agentsdk.MCPAuthToken) || string(server.BindingMode) != string(agentsdk.BindingShared) || string(server.AuthInjection.Type) != string(agentsdk.AuthInjectAPIKey) || server.AuthInjection.Name != "x-api-key" {
		t.Fatalf("MCP declaration mismatch: %#v", server)
	}
	wanted := map[string]bool{"compare_interest": true, "regional_interest": true, "related_queries": true}
	for _, tool := range m.Tools {
		if !wanted[tool.Name] || string(tool.Access) != string(agentsdk.AccessUser) {
			t.Fatalf("unexpected tool %s/%s", tool.Name, tool.Access)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatal("missing output properties")
		}
		for _, key := range []string{"request", "source"} {
			obj, ok := properties[key].(map[string]any)
			if !ok || obj["type"] != "object" {
				t.Fatalf("%s.%s must be an object", tool.Name, key)
			}
		}
	}
	for _, route := range m.Routes {
		if route.Path == "/" || route.Path == "/query" {
			if string(route.Access) != string(agentsdk.AccessUser) {
				t.Fatal("public app route")
			}
		}
	}
}
