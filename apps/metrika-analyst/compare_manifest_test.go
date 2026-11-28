package main

import "testing"

func TestComparePeriodsReadOnlyManifest(t *testing.T) {
	for _, definition := range newAgent().Manifest().Tools {
		if definition.Name != "compare_periods" {
			continue
		}
		if len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
			t.Fatal("compare_periods must expose a read-only typed cross-app contract")
		}
		return
	}
	t.Fatal("missing compare_periods")
}
