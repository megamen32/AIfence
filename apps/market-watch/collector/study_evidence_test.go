package catalog

import "testing"

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
