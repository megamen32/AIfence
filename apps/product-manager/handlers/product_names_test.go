package handlers

import (
	"agent/internal/evidence"
	"testing"
)

func TestCatalogFamilyNamesAndLegacyProof(t *testing.T) {
	legacy := monitorObservation{Row: evidence.Row{Product: "demo_target"}}
	if got := observedProductName(legacy.Product, legacy); got != unknownFamily {
		t.Fatalf("unproved migration displayed %q", got)
	}
	legacy.details.Origin = "imported_historical"
	if got := observedProductName(legacy.Product, legacy); got != unknownFamily {
		t.Fatalf("historical data used a fixture migration: %q", got)
	}
	legacy.details.Origin = "synthetic_fixture"
	if got := observedProductName(legacy.Product, legacy); got != "Kaspersky Small Office Security" {
		t.Fatal(got)
	}
	if got := observedProductName("demo_control", legacy); got != "Dr.Web Desktop Security Suite" {
		t.Fatal(got)
	}
	if got := observedProductName("kaspersky_endpoint_security", legacy); got != "Kaspersky Endpoint Security" {
		t.Fatalf("an actual KES key was remapped: %q", got)
	}
	if got := productName(ksosFamily); got != "Kaspersky Small Office Security" {
		t.Fatal(got)
	}
	if got := productName(drWebFamily); got != "Dr.Web Desktop Security Suite" {
		t.Fatal(got)
	}
}

func TestKnownSKUDisplayUsesExactCatalogIdentity(t *testing.T) {
	for sku, title := range map[string]string{
		"KL4141RCEFR":       "Kaspersky Small Office Security",
		"LBW-BC-12M-100-A3": "Dr.Web Desktop Security Suite",
	} {
		row := monitorObservation{snapshot: "catalog", Row: evidence.Row{
			Source: "market", Kind: "offer", Metric: "displayed_price", Status: "observed",
			Product: sku, Unit: "RUB", Value: "41", Period: evidence.Period{To: "2026-01-03"},
			Details: `{"seller":"softline","price_basis":"displayed_total","quantity":1}`,
		}}
		if got := currentPrices(sku, row.snapshot, []monitorObservation{row}, nil); got == nil || got.Name != title || got.SKU != sku {
			t.Fatalf("%s display: %+v", sku, got)
		}
	}
}
