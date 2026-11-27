package catalog

import (
	"strings"
	"testing"
)

func TestStudyRanksPublishedPriceLinks(t *testing.T) {
	s := Source{ID: "test", Host: "shop.example.com"}
	u := "https://shop.example.com/catalog/"
	raw := []byte(`<html><body><nav><a href="/news/">News</a></nav><div class="product"><a href="/item/a/">License</a><span class="price">123.45</span></div><a href="/other/">Other</a></body></html>`)
	hints := StudyLinkScores(s, u, raw)
	good := "https://shop.example.com/item/a/"
	if hints[good] <= 0 || hints["https://shop.example.com/news/"] != 0 {
		t.Fatal(hints)
	}
	urls := []string{"https://shop.example.com/other/", good}
	RankStudyURLs(urls, hints)
	if urls[0] != good {
		t.Fatal(urls)
	}
}

func TestVisibleSeedUsesActualMarkup(t *testing.T) {
	u := "https://shop.example.com/item/"
	s := Source{ID: "shop", Host: "shop.example.com"}
	raw := []byte(`<html><head><link rel="canonical" href="/item/"></head><body><main class="details"><h1>License</h1><div class="pd-sku">Артикул <code>ABC</code></div><div class="pd-price"><s>341 600 ₽</s><b class="pr">170 800 ₽</b></div></main></body></html>`)
	c, ok := VisibleAdapterSeed(s, AdapterSample{URL: u, Body: raw})
	if !ok {
		t.Fatal("did not derive actual price selector")
	}
	d := EvaluateAdapter(s, AdapterSpec{Cases: []AdapterCase{c}}, nil, []AdapterSample{{URL: u, Body: raw}, {URL: "https://shop.example.com/another/", Body: []byte(strings.ReplaceAll(string(raw), "/item/", "/another/")), Holdout: true}})
	if !d.Accepted || d.TrainingPrices != 1 || d.HoldoutPrices != 1 {
		t.Fatal(d, c)
	}
}
