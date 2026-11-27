package catalog

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Discover from publisher-observed URLs only. A model can propose selectors
// and path filters; it cannot invent an endpoint or enlarge the approved host.
func PublishedLinks(s Source, page string, body []byte) []string {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return nil
	}
	type candidate struct {
		url   string
		score int
	}
	seen := map[string]bool{}
	var candidates []candidate
	doc.Find("a[href]").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		raw, _ := n.Attr("href")
		u, ok := seedLink(s, page, raw)
		if !ok || u == page || seen[u] || PublicStoreURL(u) != nil {
			return true
		}
		p, e := url.Parse(u)
		if e != nil {
			return true
		}
		lower := strings.ToLower(p.Path)
		for _, x := range []string{"/cart", "/basket", "/login", "/account", "/auth", "/checkout", "/register", "/privacy", "/contacts", "/delivery", "/about", "/news", "/blog", "/search", "/compare", "/wishlist", "/personal", ".pdf", ".jpg", ".png", ".zip", ".exe"} {
			if strings.Contains(lower, x) {
				return true
			}
		}
		score := strings.Count(strings.Trim(p.Path, "/"), "/") * 2
		for _, k := range []string{"product", "catalog", "software", "tovar", "license", "licens", "soft", "kaspersky", "drweb", "1c"} {
			if strings.Contains(lower, k) {
				score += 3
			}
		}
		parent, _ := n.Parent().Attr("class")
		self, _ := n.Attr("class")
		if strings.Contains(strings.ToLower(parent+" "+self), "product") {
			score += 8
		}
		if n.Parent().Find("[itemprop=price],[class*=price]").Length() > 0 {
			score += 8
		}
		if p.RawQuery != "" {
			score -= 3
		}
		seen[u] = true
		candidates = append(candidates, candidate{u, score})
		return len(candidates) < 3000
	})
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.url)
	}
	return out
}

func PublishedSitemaps(s Source, body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		key, raw, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(key, "sitemap") {
			u := strings.TrimSpace(raw)
			if Allowed(s, u) && PublicStoreURL(u) == nil && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}
func ReadPublicSitemap(s Source, b []byte) (pages, children []string, err error) {
	var m Sitemap
	d := xml.NewDecoder(bytes.NewReader(b))
	if e := d.Decode(&m); e != nil {
		return nil, nil, e
	}
	if m.XMLName.Local != "urlset" && m.XMLName.Local != "sitemapindex" {
		return nil, nil, fmt.Errorf("not a public sitemap")
	}
	for _, v := range m.Entries {
		if Allowed(s, v.Loc) && PublicStoreURL(v.Loc) == nil {
			pages = append(pages, v.Loc)
		}
	}
	for _, v := range m.Children {
		if Allowed(s, v.Loc) && PublicStoreURL(v.Loc) == nil {
			children = append(children, v.Loc)
		}
	}
	return pages, children, nil
}
func controlPause(err error) bool {
	var status *HTTPStatus
	return errors.As(err, &status) && (status.Code == 403 || status.Code == 429 || status.Code == 503 || status.RetryAfter.After(time.Now()))
}

type StoreProbe struct {
	Samples  []AdapterSample
	Frontier []string
	Sitemaps []string
	Errors   []string
}
type Capture func(context.Context, AdapterSample) error

// Bounded onboarding. All fetched evidence is archived by the caller before
// it enters the learning corpus. Full sitemap discovery is intentionally
// separate from this initial study.
func ProbeStore(ctx context.Context, s Source, root string, fetch Fetch, capture Capture, limit int) (StoreProbe, error) {
	var out StoreProbe
	if limit < 3 || limit > 20 {
		return out, fmt.Errorf("probe limit must be 3..20")
	}
	seen := map[string]bool{}
	queued := map[string]bool{}
	var q []string
	enqueue := func(urls []string) {
		for _, u := range urls {
			if Allowed(s, u) && PublicStoreURL(u) == nil && !queued[u] && len(q) < 5000 {
				queued[u] = true
				q = append(q, u)
			}
		}
	}
	var capturedAt time.Time
	read := func(u string) ([]byte, error) {
		seen[u] = true
		b, e := fetch(ctx, s, u)
		if e != nil {
			return nil, e
		}
		capturedAt = time.Now().UTC()
		sample := AdapterSample{URL: u, Body: b, CapturedAt: capturedAt}
		if capture != nil {
			if e = capture(ctx, sample); e != nil {
				return nil, e
			}
		}
		return b, nil
	}
	raw, e := read(root)
	if e != nil {
		return out, e
	}
	out.Samples = append(out.Samples, AdapterSample{URL: root, Body: raw, CapturedAt: capturedAt})
	enqueue(PublishedLinks(s, root, raw))
	robots := "https://" + s.Host + "/robots.txt"
	if b, e := read(robots); e == nil {
		maps := PublishedSitemaps(s, b)
		out.Sitemaps = append(out.Sitemaps, maps...)
		// Inspect at most three publisher-announced sitemap documents. Prefer
		// product documents if the index exposes distinct sitemap kinds.
		sort.SliceStable(maps, func(i, j int) bool { return sitemapScore(maps[i]) > sitemapScore(maps[j]) })
		var fromMaps []string
		for n := 0; n < 3 && len(maps) > 0; n++ {
			u := maps[0]
			maps = maps[1:]
			if seen[u] {
				n--
				continue
			}
			b, e := read(u)
			if e != nil {
				if controlPause(e) {
					return out, e
				}
				out.Errors = append(out.Errors, u+": "+e.Error())
				continue
			}
			pages, children, e := ReadPublicSitemap(s, b)
			if e != nil {
				out.Errors = append(out.Errors, u+": "+e.Error())
				continue
			}
			// Evenly spread samples over the publisher's inventory rather than
			// taking the first manufacturer's URLs only.
			if len(pages) > 0 {
				n := min(120, len(pages))
				for i := 0; i < n; i++ {
					fromMaps = append(fromMaps, pages[i*len(pages)/n])
				}
			}
			maps = append(maps, children...)
			sort.SliceStable(maps, func(i, j int) bool { return sitemapScore(maps[i]) > sitemapScore(maps[j]) })
		}
		if len(fromMaps) > 0 {
			old := q
			q = nil
			queued = map[string]bool{}
			enqueue(fromMaps)
			enqueue(old)
		}
	} else if controlPause(e) {
		return out, e
	}
	for attempts := 0; len(out.Samples) < limit && len(q) > 0 && attempts < limit+5; attempts++ {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		u := q[0]
		q = q[1:]
		if seen[u] {
			continue
		}
		b, e := read(u)
		if e != nil {
			if controlPause(e) {
				return out, e
			}
			out.Errors = append(out.Errors, u+": "+e.Error())
			continue
		}
		out.Samples = append(out.Samples, AdapterSample{URL: u, Body: b, CapturedAt: capturedAt})
		enqueue(PublishedLinks(s, u, b))
	}
	out.Frontier = q
	// Fixed, URL-disjoint holdout. The model receives no holdout body.
	for i := range out.Samples {
		out.Samples[i].Holdout = i%3 == 2
	}
	return out, nil
}
func sitemapScore(s string) int {
	s = strings.ToLower(s)
	score := 0
	for _, v := range []string{"product", "catalog", "software", "tovar", "offer", "soft"} {
		if strings.Contains(s, v) {
			score += 10
		}
	}
	for _, v := range []string{"news", "blog", "image", "video"} {
		if strings.Contains(s, v) {
			score -= 10
		}
	}
	return score
}
