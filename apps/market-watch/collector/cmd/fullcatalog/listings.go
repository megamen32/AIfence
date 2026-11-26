package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	catalog "agent/collector"
)

type listingVisit struct {
	Next string `json:"next,omitempty"`
	At   string `json:"at"`
}

func vendorRoots(urls []string) []string {
	seen := map[string]bool{}
	roots := []string{}
	for _, raw := range urls {
		u, e := url.Parse(raw)
		if e != nil {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 {
			continue
		}
		u.Path = "/" + parts[0] + "/"
		u.RawPath = ""
		u.RawQuery = ""
		link := u.String()
		if !seen[link] {
			seen[link] = true
			roots = append(roots, link)
		}
	}
	sort.Strings(roots)
	return roots
}

// Listings are an early snapshot. Canonical family pages are still reconciled
// individually, including listings where a published variant or SKU is missing.
func collectListings(ctx context.Context, s catalog.Source, dir string, inv inventory, pages map[string]catalog.PageResult, fetch catalog.Fetch, save func(catalog.PageResult) error) error {
	path := filepath.Join(dir, "listing-progress.json")
	visits := map[string]listingVisit{}
	if data, e := os.ReadFile(path); e == nil {
		if e = json.Unmarshal(data, &visits); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	byCanonical := map[string]string{}
	for _, v := range inv.URLs {
		u, e := url.Parse(v)
		if e == nil {
			byCanonical[u.String()] = v
		}
	}
	// Start with unvisited vendors that expose many software families. Mixed
	// hardware/software vendors may have hundreds of irrelevant listing pages.
	remaining := map[string]int{}
	for _, u := range inv.URLs {
		if cached, ok := pages[u]; ok && cached.At >= inv.DiscoveredAt {
			continue
		}
		for _, vendor := range vendorRoots([]string{u}) {
			remaining[vendor]++
		}
	}
	vendors := vendorRoots(inv.URLs)
	sort.SliceStable(vendors, func(i, j int) bool { return remaining[vendors[i]] > remaining[vendors[j]] })
	for _, vendor := range vendors {
		if remaining[vendor] < 2 {
			continue
		}
		limit := remaining[vendor]
		seen := map[string]bool{}
		misses := 0
		for current := vendor; current != "" && !seen[current] && len(seen) < limit && remaining[vendor] > 0 && misses < 2; {
			seen[current] = true
			if visit, ok := visits[current]; ok && visit.At >= inv.DiscoveredAt {
				current = visit.Next
				continue
			}
			at := time.Now().UTC()
			b, e := fetch(ctx, s, current)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e != nil {
				var he *catalog.HTTPStatus
				if errors.As(e, &he) && (he.Code == 403 || he.Code == 429 || he.Code == 503) {
					if he.Code == 403 && he.URL == current {
						visits[current] = listingVisit{At: at.Format(time.RFC3339)}
						if err := saveJSON(path, visits); err != nil {
							return err
						}
					}
					return e
				}
				// Listing is optional. Canonical product URLs remain in the main queue.
				visits[current] = listingVisit{At: at.Format(time.RFC3339)}
				if err := saveJSON(path, visits); err != nil {
					return err
				}
				break
			}
			rows, next, e := catalog.VendorListing(s, current, bytes.NewReader(b), at)
			if e != nil {
				return e
			}
			hits := 0
			for _, p := range rows {
				indexed, ok := byCanonical[p.URL]
				if !ok {
					continue
				}
				p.URL = indexed
				hits++
				if cached, ok := pages[indexed]; ok && cached.At >= inv.DiscoveredAt {
					continue
				}
				if e = save(p); e != nil {
					return e
				}
				remaining[vendor]--
			}
			if hits == 0 {
				misses++
			} else {
				misses = 0
			}
			visits[current] = listingVisit{At: at.Format(time.RFC3339), Next: next}
			if e = writeAtomic(path, func(w io.Writer) error { return json.NewEncoder(w).Encode(visits) }); e != nil {
				return e
			}
			current = next
		}
	}
	return nil
}
