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
}

func TestPriceWeekDeltaReadOnlyManifest(t *testing.T) {
	for _, definition := range newAgent().Manifest().Tools {
		if definition.Name != "price_week_delta" {
			continue
		}
		if !definition.ReadOnly || len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
			t.Fatalf("price_week_delta must declare a typed read-only contract: %+v", definition)
		}
		return
	}
	t.Fatal("price_week_delta missing from manifest")
}
