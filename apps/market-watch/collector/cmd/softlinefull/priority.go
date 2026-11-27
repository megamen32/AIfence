package main

import (
	"agent/collector"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A slug match is a scheduling hint only. No offer, identity or price is
// created from it. The fetched card still has to prove the actual license.
func slugSKU(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	part := filepath.Base(strings.TrimSuffix(u.Path, "/"))
	i := strings.LastIndex(part, "-")
	if i <= 0 || i == len(part)-1 {
		return ""
	}
	for _, c := range part[i+1:] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return strings.ToUpper(part[:i])
}
func competitorSKUs(root string) (map[string]int, error) {
	counts := map[string]int{}
	for _, id := range []string{"softmagazin", "softmap"} {
		f, e := os.Open(filepath.Join(root, id, "offers.jsonl"))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		dec := json.NewDecoder(f)
		for {
			var o catalog.Offer
			e = dec.Decode(&o)
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				f.Close()
				return nil, e
			}
			sku := strings.ToUpper(strings.TrimSpace(o.SKU))
			if o.Source == id && sku != "" && o.PriceMinor > 0 && o.Currency == "RUB" {
				seen[sku] = true
			}
		}
		f.Close()
		for sku := range seen {
			counts[sku]++
		}
	}
	return counts, nil
}
func (s *survey) prioritizePending(pending []pageJob) {
	counts, e := competitorSKUs(filepath.Dir(s.out))
	if e != nil {
		// Priority is optional. A bad competing snapshot must not stop discovery.
		fmt.Fprintln(os.Stderr, "comparison priority unavailable:", e)
		return
	}
	scores := make(map[string]int, len(pending))
	overlap := 0
	for _, j := range pending {
		n := counts[slugSKU(j.page)]
		if n > 0 {
			overlap++
		}
		score := 2 * n
		if _, ok := s.prior[j.page]; ok {
			score++
		}
		scores[j.page] = score
	}
	sort.SliceStable(pending, func(i, j int) bool { return scores[pending[i].page] > scores[pending[j].page] })
	fmt.Printf("softline comparison-first: %d of %d pending URLs prioritized by SKU slug (not verified matches)\n", overlap, len(pending))
}
