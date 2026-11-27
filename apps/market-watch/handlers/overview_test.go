package handlers

import (
	"strings"
	"testing"

	"agent/market"
)

func change(sku, name, seller string, before, after float64) market.Observation {
	return market.Observation{ProductKey: sku, Value: after - before, SourceRef: market.SourceRef{URL: "https://" + seller + ".example/" + sku},
		Details: market.Details{Seller: seller, Name: name, PreviousPrice: &before, CurrentPrice: &after}}
}

func status() market.CatalogStatus {
	return market.CatalogStatus{Total: 1325, SKUs: 640, Sources: []market.SourceSummary{
		{Source: "softline", Offers: 700, LastObservation: "2026-11-26T10:09:00Z"},
		{Source: "allsoft", Offers: 400, LastObservation: "2026-11-27T08:30:00Z"},
		{Source: "softmap", Offers: 225},
		{Source: "syssoft"},
	}}
}

func TestOverviewTilesBarsAndTopChanges(t *testing.T) {
	ev := &market.Evidence{Observations: []market.Observation{
		change("KL4141RCEFS", "Kaspersky Small Office Security", "softline", 40089, 42093.45),
		change("DW-BOX-1", "Dr.Web Security Space", "allsoft", 1500, 1200),
		change("ALSE-1.7", "Astra Linux Special Edition", "softmap", 9000, 9090),
		change("SAME", "Без изменений", "softmap", 100, 100),
	}}
	o := buildOverview(status(), ev)
	if o == nil || len(o.Tiles) != 4 {
		t.Fatalf("tiles: %+v", o)
	}
	want := []string{"1 325", "3", "640", "3"}
	for i, w := range want {
		if o.Tiles[i].Value != w {
			t.Errorf("tile %d = %q, want %q", i, o.Tiles[i].Value, w)
		}
	}
	if !strings.Contains(o.Tiles[3].Note, "2 выросли") || !strings.Contains(o.Tiles[3].Note, "1 снизились") {
		t.Errorf("note %q", o.Tiles[3].Note)
	}
	if len(o.Bars) != 3 || o.Bars[0].Label != "Softline" || o.Bars[1].Label != "Allsoft" {
		t.Fatalf("bars %+v", o.Bars)
	}
	if len(o.Changes) != 3 || o.Changes[0].SKU != "DW-BOX-1" || o.Changes[0].Percent != "−20,0 %" || o.Changes[0].Tone != "down" {
		t.Fatalf("changes %+v", o.Changes)
	}
	if o.Changes[1].Percent != "+5,0 %" || o.Changes[1].Tone != "up" || o.Changes[1].Old != "40 089 ₽" || o.Changes[1].New != "42 093,45 ₽" {
		t.Fatalf("second %+v", o.Changes[1])
	}
	if !strings.HasPrefix(o.Changes[0].URL, "./?q=DW-BOX-1&tab=changes") {
		t.Errorf("url %q", o.Changes[0].URL)
	}
	if !strings.Contains(o.Source, "27.11.2026 08:30 UTC") {
		t.Errorf("source %q", o.Source)
	}
}

func TestOverviewEmptyWeekAndMissingValues(t *testing.T) {
	s := status()
	s.SKUs = 0
	o := buildOverview(s, &market.Evidence{})
	if !o.ShowChanges || len(o.Changes) != 0 || len(o.Tiles) != 3 || o.Tiles[2].Value != "0" {
		t.Fatalf("%+v", o)
	}
	if o := buildOverview(s, nil); o.ShowChanges || len(o.Tiles) != 2 {
		t.Fatalf("no lookup: %+v", o)
	}
	if buildOverview(market.CatalogStatus{}, nil) != nil {
		t.Fatal("empty store must have no overview")
	}
}

func TestOverviewFoldsSmallStoresIntoOthers(t *testing.T) {
	var s market.CatalogStatus
	for i, id := range []string{"softline", "allsoft", "softmap", "syssoft", "softmagazin", "a", "b", "c"} {
		s.Sources = append(s.Sources, market.SourceSummary{Source: id, Offers: int64(100 - i)})
		s.Total += int64(100 - i)
	}
	o := buildOverview(s, nil)
	if len(o.Bars) != 6 || o.Bars[5].Label != "Другие" || o.Bars[5].Count != "282" {
		t.Fatalf("%+v", o.Bars)
	}
}
