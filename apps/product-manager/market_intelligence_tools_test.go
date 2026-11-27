package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/agentsdk/wire"
)

func TestMarketIntelligenceToolContracts(t *testing.T) {
	a := agentsdk.New(agentsdk.Config{Description: "test"})
	registerMarketIntelligence(a, &evidence.Service{})
	manifest := a.Manifest()
	for _, name := range []string{"read_wordstat", "read_google_trends", "read_competitor_prices"} {
		index := slices.IndexFunc(manifest.Tools, func(item wire.ToolDef) bool { return item.Name == name })
		if index < 0 {
			t.Fatalf("missing tool %s", name)
		}
		item := manifest.Tools[index]
		if item.Access != wire.AccessUser || !strings.Contains(item.Description, "imported Product Manager snapshots") || !strings.Contains(item.Description, "No live network request") {
			t.Fatalf("unsafe read contract for %s: %+v", name, item)
		}
		schema := string(item.InputSchema)
		for _, field := range []string{"product_key", "snapshot_id", "query", "offset", "limit"} {
			if !strings.Contains(schema, field) {
				t.Errorf("%s schema misses %s: %s", name, field, schema)
			}
		}
		output := string(item.OutputSchema)
		for _, field := range []string{"source_ref", "origin", "synthetic", "period", "available_products"} {
			if !strings.Contains(output, field) {
				t.Errorf("%s output misses %s: %s", name, field, output)
			}
		}
		if strings.Contains(output, "cause") || strings.Contains(output, "oracle") {
			t.Fatalf("%s output exposes a conclusion field: %s", name, output)
		}
		if strings.Contains(output, "authentic_api") {
			t.Fatalf("%s output turns a declared source into an authenticity claim: %s", name, output)
		}
	}
}

func TestDemandReadersAcceptOnlyDeclaredImportedSources(t *testing.T) {
	rows := []evidence.Row{
		intelligenceRow("wordstat-api", "research", "metric", "search_queries", "42", "count", "yandex-wordstat:search-api", `{"origin":"authorized_import","synthetic":false,"normalization":"exact phrase","geo":"RU"}`),
		intelligenceRow("wordstat-other", "research", "metric", "search_queries", "99", "count", "other", `{}`),
		intelligenceRow("trends", "research", "metric", "search_interest", "87", "index", "google_trends", `{"origin":"synthetic_fixture","synthetic":true,"geo":"RU-MOW"}`),
		intelligenceRow("bad-index", "research", "metric", "search_interest", "101", "index", "google_trends", `{}`),
		intelligenceRow("fractional-count", "research", "metric", "search_queries", "1.5", "count", "wordstat", `{}`),
		intelligenceRow("not-a-number", "research", "metric", "search_queries", "NaN", "count", "wordstat", `{}`),
	}
	wordstat := selectIntelligenceRows(rows, intelligenceSpec{source: "research", metric: "search_queries", unit: "count", sourceIDs: []string{"wordstat", "yandex-wordstat:search-api"}})
	if len(wordstat) != 1 || wordstat[0].SourceRef.SourceID != "yandex-wordstat:search-api" || wordstat[0].Origin != "authorized_import" || wordstat[0].Geo != "RU" || wordstat[0].Synthetic {
		t.Fatalf("unexpected Wordstat rows: %+v", wordstat)
	}
	trends := selectIntelligenceRows(rows, intelligenceSpec{source: "research", metric: "search_interest", unit: "index", sourceIDs: []string{"google_trends"}})
	if len(trends) != 1 || trends[0].Value != 87 || !trends[0].Synthetic || trends[0].Geo != "RU-MOW" {
		t.Fatalf("unexpected Trends rows: %+v", trends)
	}
}

func TestCompetitorPricesKeepUnknownQuantitiesIncomparable(t *testing.T) {
	rows := []evidence.Row{
		intelligenceRow("known", "market", "offer", "displayed_price", "4000.5", "RUB", "softline", `{"name":"Endpoint license","seller":"Softline","price_basis":"per_unit","quantity":10,"max_quantity":14,"captured_at":"2026-11-26T08:00:00Z","origin":"authorized_import","market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("unknown", "market", "offer", "displayed_price", "4445", "RUB", "softmagazin", `{"name":"Endpoint license","seller":"softmagazin","price_basis":"displayed_offer_price","captured_at":"2026-11-26T08:05:00Z"}`),
		intelligenceRowWithStatus("source-incomparable", "market", "offer", "displayed_price", "4100", "RUB", "store", "incomparable", `{"price_basis":"displayed_total","quantity":1,"market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("per-unit-no-max", "market", "offer", "displayed_price", "4200", "RUB", "store", `{"price_basis":"per_unit","quantity":10,"market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("zero-quantity", "market", "offer", "displayed_price", "4300", "RUB", "store", `{"price_basis":"displayed_total","quantity":0,"market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("zero-price", "market", "offer", "displayed_price", "0", "RUB", "store", `{"price_basis":"displayed_total","quantity":1,"market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("bad-currency", "market", "offer", "displayed_price", "100", "RUBLE", "store", `{"price_basis":"displayed_total","quantity":1,"market_comparison":{"group":"same-license","comparable":true}}`),
		intelligenceRow("wrong-kind", "market", "metric", "displayed_price", "100", "RUB", "store", `{}`),
	}
	prices := selectIntelligenceRows(rows, intelligenceSpec{source: "market", metric: "displayed_price", market: true})
	if len(prices) != 5 || !prices[0].ComparableTerms || prices[1].ComparableTerms || prices[2].ComparableTerms || prices[3].ComparableTerms || prices[4].ComparableTerms || prices[0].Quantity == nil || *prices[0].Quantity != 10 {
		t.Fatalf("unexpected price rows: %+v", prices)
	}
	if prices[0].SourceRef.URL == "" || prices[1].SourceRef.Locator == "" || prices[0].Seller != "Softline" || prices[1].PriceBasis == "" || prices[2].Status != "incomparable" || prices[2].ComparisonReason == "" || prices[3].ComparisonReason == "" || prices[4].ComparisonReason == "" {
		t.Fatal("price identity, terms or source locator was lost")
	}
}

func TestSnapshotWarningsDoNotStopAutomaticSearch(t *testing.T) {
	in := marketIntelligenceInput{ProductKey: "kaspersky_endpoint_security"}
	if snapshotSearchComplete(in, marketIntelligenceResult{Status: "unavailable", Warnings: []string{"other source unavailable"}}) {
		t.Fatal("warning from an irrelevant snapshot stopped automatic search")
	}
	if !snapshotSearchComplete(in, marketIntelligenceResult{Status: "available"}) || !snapshotSearchComplete(marketIntelligenceInput{SnapshotID: "chosen"}, marketIntelligenceResult{Status: "unavailable"}) {
		t.Fatal("available or explicitly selected snapshot did not stop search")
	}
}

func TestAvailableProductSearchAndPagination(t *testing.T) {
	rows := []marketIntelligenceObservation{{ProductKey: "kaspersky_endpoint_security", Name: "Kaspersky Endpoint Security"}, {ProductKey: "astra_linux", Name: "Astra Linux"}, {ProductKey: "kaspersky_endpoint_security"}}
	if got := availableProducts(rows, "endpoint"); !slices.Equal(got, []string{"kaspersky_endpoint_security"}) {
		t.Fatalf("search = %v", got)
	}
	start, end := pageBounds(1, 1, 2)
	if start != 1 || end != 2 || nextOffset(end, 2) != nil {
		t.Fatalf("bounds = %d..%d", start, end)
	}
}

func intelligenceRow(id, source, kind, metric, value, unit, sourceID, details string) evidence.Row {
	return intelligenceRowWithStatus(id, source, kind, metric, value, unit, sourceID, "observed", details)
}

func intelligenceRowWithStatus(id, source, kind, metric, value, unit, sourceID, status, details string) evidence.Row {
	var valid map[string]any
	if json.Unmarshal([]byte(details), &valid) != nil {
		panic("invalid test details")
	}
	return evidence.Row{
		ID: id, Source: source, Kind: kind, Metric: metric, Product: "kaspersky_endpoint_security", Value: value, Unit: unit, Status: status,
		Period: evidence.Period{From: "2026-11-01", To: "2026-11-30"},
		Ref:    evidence.SourceRef{SourceID: sourceID, Locator: "row/" + id, URL: "https://example.invalid/" + id}, Details: details,
	}
}
