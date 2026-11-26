package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	catalog "agent/collector"
	"github.com/PuerkitoBio/goquery"
)

type categoryAudit struct {
	InventoryAt string                     `json:"inventory_at"`
	Complete    bool                       `json:"complete"`
	Errors      map[string]string          `json:"errors,omitempty"`
	Sections    map[string]sectionEvidence `json:"sections,omitempty"`
}
type sectionEvidence struct {
	Published int         `json:"published_families"`
	Observed  int         `json:"observed_families"`
	Pages     int         `json:"pages"`
	Counts    map[int]int `json:"visible_totals_and_page_counts"`
	Empty     []string    `json:"empty_page_urls,omitempty"`
}
type listingVisit struct {
	Next     string   `json:"next,omitempty"`
	At       string   `json:"at"`
	Issue    string   `json:"issue,omitempty"`
	Verified bool     `json:"verified"`
	Recheck  bool     `json:"recheck,omitempty"`
	Total    int      `json:"published_total"`
	Families []string `json:"families,omitempty"`
}

func softwareSections(s catalog.Source, body []byte) ([]string, error) {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	root, _ := url.Parse("https://" + s.Host + "/soft/")
	var sections []string
	doc.Find("a.catalog-name[href]").Each(func(_ int, a *goquery.Selection) {
		raw, _ := a.Attr("href")
		u, e := root.Parse(raw)
		if e != nil || !catalog.Allowed(s, u.String()) || u.RawQuery != "" {
			return
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 && parts[0] == "soft" {
			sections = append(sections, u.String())
		}
	})
	sections = unique(sections)
	if len(sections) == 0 {
		return nil, fmt.Errorf("software catalog lists no top-level categories")
	}
	return sections, nil
}

// Only the publisher's software sections are used for early price snapshots.
// Hardware also appears in the family sitemap and on general vendor lists.
func collectListings(ctx context.Context, s catalog.Source, dir string, inv *inventory, pages map[string]catalog.PageResult, fetch catalog.Fetch, save func(catalog.PageResult) error) error {
	path := filepath.Join(dir, "software-listing-progress.json")
	visits := map[string]listingVisit{}
	if data, e := os.ReadFile(path); e == nil {
		if e = json.Unmarshal(data, &visits); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	audit := categoryAudit{InventoryAt: inv.DiscoveredAt, Errors: map[string]string{}, Sections: map[string]sectionEvidence{}}
	auditFile := filepath.Join(dir, "category-audit.json")
	if err := saveJSON(auditFile, audit); err != nil {
		return err
	}
	root := "https://" + s.Host + "/soft/"
	body, e := fetch(ctx, s, root)
	if e != nil {
		return e
	}
	sections, e := softwareSections(s, body)
	if e != nil {
		return e
	}
	indexed := map[string]string{}
	for _, v := range inv.URLs {
		u, e := url.Parse(v)
		if e == nil {
			indexed[u.String()] = v
		}
	}
	for _, section := range sections {
		seen := map[string]bool{}
		families := map[string]bool{}
		published := -1
		counts := map[int]int{}
		var empty []string
		observe := func(current string, v listingVisit) {
			if v.Issue != "" {
				return
			}
			counts[v.Total]++
			if len(v.Families) == 0 {
				empty = append(empty, current)
			}
			if published < 0 {
				published = v.Total
			} else if v.Total != published {
				audit.Errors[section] = fmt.Sprintf("category count changed during pagination from %d to %d", published, v.Total)
			}
			for _, u := range v.Families {
				families[u] = true
			}
		}
		for current := section; current != "" && !seen[current]; {
			seen[current] = true
			if visit, ok := visits[current]; ok && visit.At >= inv.DiscoveredAt && visit.Verified && !visit.Recheck {
				observe(current, visit)
				if visit.Issue != "" {
					audit.Errors[current] = visit.Issue
				}
				current = visit.Next
				continue
			}
			at := time.Now().UTC()
			body, e = fetch(ctx, s, current)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e != nil {
				// The family sitemap still provides the canonical detail URLs.
				var status *catalog.HTTPStatus
				if errors.As(e, &status) && status.Code == 403 && status.URL == current {
					visits[current] = listingVisit{At: at.Format(time.RFC3339), Issue: e.Error(), Verified: true}
					if err := saveJSON(path, visits); err != nil {
						return err
					}
				}
				if errors.As(e, &status) && (status.Code == 403 || status.Code == 429 || status.Code == 503) {
					return e
				}
				audit.Errors[current] = e.Error()
				break
			}
			listing, e := catalog.ReadSoftwareListing(s, current, bytes.NewReader(body), at)
			if e != nil {
				audit.Errors[current] = e.Error()
				break
			}
			rows, next := listing.Rows, listing.Next
			visit := listingVisit{At: at.Format(time.RFC3339), Next: next, Verified: true, Total: listing.Total}
			for _, p := range rows {
				visit.Families = append(visit.Families, p.URL)
			}
			observe(current, visit)
			enlarged := false
			for _, p := range rows {
				u, e := url.Parse(p.URL)
				if e != nil || !catalog.Allowed(s, p.URL) || u.RawQuery != "" || len(strings.Split(strings.Trim(u.Path, "/"), "/")) != 2 {
					continue
				}
				key, ok := indexed[p.URL]
				if !ok {
					key = p.URL
					indexed[key] = key
					inv.URLs = append(inv.URLs, key)
					enlarged = true
				}
				p.URL = key
				cached, ok := pages[key]
				if ok && cached.At >= inv.DiscoveredAt {
					if cached.Category == "hardware" || cached.Category == "software" {
						continue
					}
					if cached.EvidenceURL == "" && (cached.State == "priced" || cached.State == "no_public_price") {
						cached.Category = "software"
						cached.CategoryURL = current
						p = cached
					}
				}
				if e = save(p); e != nil {
					return e
				}
			}
			if enlarged {
				if e = saveJSON(filepath.Join(dir, "inventory.json"), inv); e != nil {
					return e
				}
			}
			visits[current] = visit
			if e = saveJSON(path, visits); e != nil {
				return e
			}
			current = next
		}
		audit.Sections[section] = sectionEvidence{Published: published, Observed: len(families), Pages: len(seen), Counts: counts, Empty: empty}
		if published < 0 || len(families) != published || len(counts) > 1 {
			audit.Errors[section] = fmt.Sprintf("visible totals (by number of pages): %v, distinct listed families: %d", counts, len(families))
			// A subsequent audit should refetch this section after page drift.
			for page := range seen {
				if v, ok := visits[page]; ok && v.Issue == "" {
					v.Recheck = true
					visits[page] = v
				}
			}
			if err := saveJSON(path, visits); err != nil {
				return err
			}
		}
		if err := saveJSON(auditFile, audit); err != nil {
			return err
		}
	}
	audit.Complete = len(audit.Errors) == 0
	return saveJSON(auditFile, audit)
}
