package market

import (
	catalog "agent/collector"
	"testing"
)

func TestImportRecognizesUnkeyedQuantityTiers(t *testing.T) {
	sources, err := catalog.Sources()
	if err != nil {
		t.Fatal(err)
	}
	min, max := int64(5), int64(9)
	row := catalog.Offer{Source: "softline", Name: "Test license", URL: "https://store.softline.ru/vendor/license/", Quantity: &min, MaxQuantity: &max, PriceBasis: "per_unit", Currency: "RUB", PriceMinor: 10900, CapturedAt: "2026-11-26T01:00:00Z"}
	if err := checkBatch([]catalog.Offer{row}, sources); err != nil {
		t.Fatal(err)
	}
	record := obs(row)
	if record.Details.MatchStatus != "source_item_only" || record.Details.MaxQuantity == nil || *record.Details.MaxQuantity != 9 {
		t.Fatal(record)
	}
	min = 10
	if err := checkBatch([]catalog.Offer{row}, sources); err == nil {
		t.Fatal("accepted reversed tier")
	}
	min = 5
	row.URL = "https://example.test/alternate/"
	if err := checkBatch([]catalog.Offer{row}, sources); err == nil {
		t.Fatal("accepted a URL not belonging to the declared store")
	}
}
