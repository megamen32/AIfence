package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	collector "agent/collector"
)

func TestBoundedExistingFeedRefresh(t *testing.T) {
	root, out := filepath.Join(t.TempDir(), "feed"), filepath.Join(t.TempDir(), "slice")
	for _, source := range []string{"softmagazin", "softmap", "softline"} {
		path := filepath.Join(root, source, "offers.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		var rows []byte
		if source == "softline" {
			o := collector.Offer{Source: source, SKU: "KL4141RCEFS", Name: "Kaspersky Small Office Security", URL: "https://store.softline.ru/kaspersky/kl4141rcefs-248970/"}
			rows, _ = json.Marshal(o)
			rows = append(rows, 10)
		}
		if err := os.WriteFile(path, rows, 0600); err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	fetch := func(_ context.Context, src collector.Source, url string) ([]byte, error) {
		requests++
		if src.ID != "softline" || url != "https://store.softline.ru/kaspersky/kl4141rcefs-248970/" {
			t.Fatalf("unexpected feed request %s %s", src.ID, url)
		}
		return []byte(`<h1>Kaspersky Small Office Security</h1><button class="js-sku-btn" data-code="KL4141RCEFS"></button><span class="js-price-with-discount">6739.20</span>`), nil
	}
	got, err := collect(t.Context(), root, out, "KL4141RCEFS,KL4210RAKDS", "", 1, fetch)
	if err != nil || requests != 1 || got.Pages != 1 || got.Offers != 1 || len(got.Missing) != 1 || got.Missing[0] != "KL4210RAKDS" {
		t.Fatalf("wrong bounded result %+v %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(out, "softline", "offers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var offer collector.Offer
	if err := json.Unmarshal(data, &offer); err != nil || offer.SKU != "KL4141RCEFS" || offer.PriceMinor != 673920 {
		t.Fatalf("wrong imported offer %+v %v", offer, err)
	}
}
