package main

import (
	"github.com/airlockrun/agentsdk/wire"
	"testing"
)

func TestAgentComposition(t *testing.T) {
	m := newAgent().Manifest()
	if err := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(m.Routes) < 2 || len(m.Tools) < 3 {
		t.Fatal("missing runtime declarations")
	}
	seen := map[string]bool{}
	for _, tool := range m.Tools {
		seen[tool.Name] = true
	}
	if !seen["price_week_delta"] || !seen["family_price_week_delta"] {
		t.Fatal("missing direct weekly price tools")
	}
}
