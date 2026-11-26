package main

import (
	catalog "agent/collector"
	"context"
	"fmt"
	"time"
)

// sitemap-4 is the publisher's mixed software/hardware product inventory.
// Never report its raw total as the number of software licenses.
func discoverSoftMagazinVariants(ctx context.Context, s catalog.Source, fetch catalog.Fetch, inv *inventory) error {
	variant := s
	variant.SitemapInclude = "/sitemap-4-[0-9]+[.]xml$"
	// The family and standalone-card URLs both have two path components.
	variant.PageInclude = "^https://www[.]softmagazin[.]ru/[^/?]+/[^/?]+/$"
	urls, e := catalog.Discover(ctx, variant, fetch)
	if e != nil {
		return e
	}
	if len(urls) == 0 {
		return fmt.Errorf("SoftMagazin product sitemap has no URLs")
	}
	inv.VariantURLs = unique(urls)
	inv.VariantSitemapAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

type variantReceipt struct {
	URL, Scope, State string
	Direct            bool
}
type variantEvidence struct {
	scope, state string
	conflict     bool
}

// Reconcile each distinct sitemap URL against actual family and direct-page
// evidence. Conflicts and missing scope stay unresolved, never guessed.
func reconcileSoftMagazin(inv inventory, pages map[string]catalog.PageResult) []variantReceipt {
	evidence := map[string]variantEvidence{}
	merge := func(u, scope, state string) {
		if u == "" || (scope != "software" && scope != "hardware") {
			return
		}
		old := evidence[u]
		if old.scope != "" && old.scope != scope {
			old.conflict = true
		}
		if old.scope == "" {
			old.scope = scope
		}
		if scope == "hardware" {
			state = "out_of_scope"
		}
		if old.state == "" || old.state == "missing_sku" || old.state == "unclassified" || old.state == "pending" {
			old.state = state
		}
		if state == "priced" {
			old.state = state
		}
		evidence[u] = old
	}
	// Use the publisher's family category, not inferred vendor names.
	for _, u := range inv.URLs {
		p, ok := pages[u]
		if !ok || p.At < inv.DiscoveredAt || p.State == "error" {
			continue
		}
		for _, v := range p.Variants {
			merge(v.URL, p.Category, v.Status)
		}
		if p.Category == "hardware" {
			for _, v := range p.ExcludedURLs {
				merge(v, "hardware", "out_of_scope")
			}
		}
	}
	out := make([]variantReceipt, 0, len(inv.VariantURLs))
	for _, u := range inv.VariantURLs {
		d, ok := pages[u]
		direct := ok && d.At >= inv.DiscoveredAt && d.EvidenceURL == ""
		f := evidence[u]
		if direct && d.State != "error" && (d.Category == "software" || d.Category == "hardware") {
			if f.scope != "" && f.scope != d.Category {
				f.conflict = true
			} else {
				f.scope = d.Category
			}
		}
		r := variantReceipt{URL: u, Scope: f.scope, State: "pending", Direct: direct}
		switch {
		case f.conflict:
			r.Scope = ""
			r.State = "category_pending"
		case f.scope == "hardware":
			r.State = "out_of_scope"
		case direct && d.State == "unavailable":
			r.State = "unavailable"
		case direct && (d.State == "access_denied" || d.State == "robots_excluded"):
			r.State = d.State
		case f.scope == "software" && (f.state == "priced" || f.state == "no_public_price"):
			r.State = f.state
		case direct && f.scope == "software" && (d.State == "priced" || d.State == "no_public_price"):
			r.State = d.State
		case direct && d.State == "error":
			if d.Attempts >= 3 {
				r.State = "error"
			}
		case direct:
			r.State = "unclassified"
		}
		out = append(out, r)
	}
	return out
}
