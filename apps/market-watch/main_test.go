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

func TestWeeklyPriceToolsTypedManifest(t *testing.T) {
	want := map[string]bool{"price_week_delta": true, "family_price_week_delta": true}
	for _, definition := range newAgent().Manifest().Tools {
		if !want[definition.Name] {
			continue
		}
		if len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
			t.Fatalf("cross-app source %s must have typed schemas", definition.Name)
		}
		delete(want, definition.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing cross-app sources: %v", want)
	}
}
