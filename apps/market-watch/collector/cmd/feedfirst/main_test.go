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
	got, err := collect(t.Context(), root, out, "KL4141RCEFS,KL4210RAKDS", "", 1, false, fetch)
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

func TestVettedCompetitorSeedsUseRemainingPageBudget(t *testing.T) {
	root, out := filepath.Join(t.TempDir(), "feed"), filepath.Join(t.TempDir(), "slice")
	for _, source := range []string{"softmagazin", "softmap", "softline"} {
		path := filepath.Join(root, source, "offers.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	responses := map[string]string{
		"allsoft": `<h1>Kaspersky Small Office Security</h1><table class="js-price_group"><tr><th><span class="b-text_product-version">License 1 year</span></th></tr><tr><td><span class="b-col_description-text">цена за копию (от 5 до 9)</span><span class="b-col_description-sku"><a href="/product/123/">KL4141RDEFS</a></span></td><td><span data-offer-price>7 488 ₽</span></td></tr></table>`,
		"syssoft": `<div class="product-edition"><h3 class="title"><a href="/product/123/">License 1 year</a></h3><div class="price"><span>Цена/шт.: <span class="price_text_rub">2 242,80 ₽</span></span></div><input name="item_value_min" value="5"/><ul class="product-edition_specs"><li><span>Артикул:</span><b>KL4546RAEFS</b></li></ul></div>`,
	}
	requests := 0
	fetch := func(_ context.Context, src collector.Source, _ string) ([]byte, error) {
		requests++
		return []byte(responses[src.ID]), nil
	}
	got, err := collect(t.Context(), root, out, "KL4141RDEFS,KL4546RAEFS", "", 2, true, fetch)
	if err != nil || requests != 2 || got.Offers != 2 || got.CandidatePages != 2 || got.Status != "partial" || got.Sources["allsoft"] != 1 || got.Sources["syssoft"] != 1 || len(got.Missing) != 0 || got.Coverage["allsoft"]["KL4141RDEFS"] != "observed" || got.Coverage["allsoft"]["KL4546RAEFS"] != "not_on_seed_page" {
		t.Fatalf("wrong five-store bounded result %+v %v", got, err)
	}
}

func TestSoftmagazinProductUsesPublishedVariantPrice(t *testing.T) {
	root, out := filepath.Join(t.TempDir(), "feed"), filepath.Join(t.TempDir(), "slice")
	url := "https://www.softmagazin.ru/laboratoriya-kasperskogo/kl4210rakde/"
	for _, source := range []string{"softmagazin", "softmap", "softline"} {
		path := filepath.Join(root, source, "offers.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		var rows []byte
		if source == "softmagazin" {
			rows, _ = json.Marshal(collector.Offer{Source: source, SKU: "KL4210RAKDE", Name: "Kaspersky Endpoint Security", URL: url})
			rows = append(rows, 10)
		}
		if err := os.WriteFile(path, rows, 0600); err != nil {
			t.Fatal(err)
		}
	}
	markup := `<div class="offer-tovar-style"><h1>Kaspersky Endpoint Security</h1><div class="articul-manufacturer"><h2><span>KL4210RAKDE</span></h2></div><div class="price-block"><span class="price-field">4 445 ₽</span></div><div class="info-category"><div class="base-links-tovar-item"><a href="/soft/security/">Security</a></div></div></div>`
	got, err := collect(t.Context(), root, out, "KL4210RAKDE", "", 1, false, func(_ context.Context, _ collector.Source, _ string) ([]byte, error) {
		return []byte(markup), nil
	})
	if err != nil || got.Sources["softmagazin"] != 1 || got.Coverage["softmagazin"]["KL4210RAKDE"] != "observed" {
		t.Fatalf("published variant price omitted: %+v %v", got, err)
	}
}
