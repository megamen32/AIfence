package catalog

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"
)

// Fetch must enforce the source allowlist, robots policy, response size,
// cancellation and pacing. Airlock can provide its native HTTP adapter.
type Fetch func(context.Context, Source, string) ([]byte, error)
type Run struct {
	Source         string   `json:"source"`
	Mode           string   `json:"mode"`
	Status         string   `json:"status"`
	Pages          int      `json:"pages"`
	CandidatePages int      `json:"candidate_pages"`
	EmptyPages     int      `json:"empty_pages"`
	Limited        bool     `json:"limited"`
	Offers         []Offer  `json:"offers"`
	Errors         []string `json:"errors,omitempty"`
}

func Discover(ctx context.Context, s Source, fetch Fetch) ([]string, error) {
	q := []string{s.Sitemap}
	seen := map[string]bool{}
	pages := map[string]bool{}
	for len(q) > 0 {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		u := q[0]
		q = q[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		if len(seen) > 100 {
			return nil, fmt.Errorf("sitemap limit exceeded")
		}
		b, e := fetch(ctx, s, u)
		if e != nil {
			return nil, e
		}
		p, c, e := ParseSitemap(s, bytes.NewReader(b))
		if e != nil {
			return nil, e
		}
		for _, v := range p {
			pages[v] = true
		}
		q = append(q, c...)
	}
	out := make([]string, 0, len(pages))
	for v := range pages {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}
func Collect(ctx context.Context, s Source, fetch Fetch, discover bool, maxPages int, at time.Time) Run {
	r := Run{Source: s.ID, Mode: "sample", Status: "ok", Offers: []Offer{}}
	pages := append([]string(nil), s.Seeds...)
	if discover {
		r.Mode = "sitemap"
		p, e := Discover(ctx, s, fetch)
		if e != nil {
			r.Status = "failed"
			r.Errors = append(r.Errors, e.Error())
			return r
		}
		seen := map[string]bool{}
		for _, v := range pages {
			seen[v] = true
		}
		for _, v := range p {
			if !seen[v] {
				pages = append(pages, v)
				seen[v] = true
			}
		}
	}
	r.CandidatePages = len(pages)
	if maxPages <= 0 {
		r.Status = "failed"
		r.Errors = append(r.Errors, "maxPages must be positive")
		return r
	}
	if len(pages) > maxPages {
		pages = pages[:maxPages]
		r.Limited = true
	}
	for _, u := range pages {
		if e := ctx.Err(); e != nil {
			r.Errors = append(r.Errors, e.Error())
			break
		}
		r.Pages++
		b, e := fetch(ctx, s, u)
		if e != nil {
			r.Errors = append(r.Errors, u+": "+e.Error())
			continue
		}
		o, e := Extract(s, u, bytes.NewReader(b), at)
		if e != nil {
			r.Errors = append(r.Errors, u+": "+e.Error())
		}
		if len(o) == 0 {
			r.EmptyPages++
		}
		r.Offers = append(r.Offers, o...)
	}
	o, e := Dedupe(r.Offers)
	if e != nil {
		r.Errors = append(r.Errors, e.Error())
		r.Offers = nil
	} else {
		r.Offers = o
	}
	if len(r.Offers) == 0 {
		r.Status = "failed"
	} else if len(r.Errors) > 0 {
		r.Status = "partial"
	} else if r.Limited {
		r.Status = "limited"
	}
	return r
}
