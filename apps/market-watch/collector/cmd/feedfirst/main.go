// feedfirst refreshes a bounded set of URLs selected from existing public
// Market Watch exports. It never discovers a sitemap or starts a background job.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	collector "agent/collector"
)

type result struct {
	RequestedSKUs  []string                     `json:"requested_skus"`
	Status         string                       `json:"status"`
	CandidatePages int                          `json:"candidate_pages"`
	Pages          int                          `json:"pages"`
	Offers         int                          `json:"offers"`
	Sources        map[string]int               `json:"sources"`
	Coverage       map[string]map[string]string `json:"coverage"`
	Missing        []string                     `json:"missing_skus"`
	Failures       map[string]string            `json:"failures"`
	CapturedAt     string                       `json:"captured_at"`
}

func main() {
	root := flag.String("root", "", "existing per-store offers.jsonl feed root")
	out := flag.String("out", "", "separate output directory for the bounded slice")
	skus := flag.String("skus", "", "comma-separated exact SKUs, up to 20")
	family := flag.String("family", "", "case-insensitive title phrase for SKU discovery")
	competitors := flag.Bool("include-competitors", false, "also check the two vetted seed-only competitor pages")
	limit := flag.Int("max-pages", 20, "maximum public pages to request, 1 to 20")
	flag.Parse()
	if *root == "" || *out == "" || *limit < 1 || *limit > 20 || (*skus == "") == (*family == "") {
		fmt.Fprintln(os.Stderr, "specify -root, -out, 1..20 max pages and exactly one of -skus or -family")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	got, err := collect(ctx, *root, *out, *skus, *family, *limit, *competitors, collector.NewHTTP().Fetch)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(got)
}

func collect(ctx context.Context, root, out, skuCSV, family string, maxPages int, includeCompetitors bool, fetch collector.Fetch) (result, error) {
	r := result{Sources: map[string]int{}, Coverage: map[string]map[string]string{}, Failures: map[string]string{}, Missing: []string{}}
	if maxPages < 1 || maxPages > 20 {
		return r, fmt.Errorf("max-pages must be 1..20")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return r, err
	}
	outPath, err := filepath.Abs(out)
	if err != nil {
		return r, err
	}
	if rootPath == outPath {
		return r, fmt.Errorf("output must be separate from the existing feed")
	}
	sources, err := collector.Sources()
	if err != nil {
		return r, err
	}
	wanted := map[string]bool{}
	for _, raw := range strings.Split(skuCSV, ",") {
		if sku := strings.ToUpper(strings.TrimSpace(raw)); sku != "" {
			wanted[sku] = true
		}
	}
	if len(wanted) > 20 {
		return r, fmt.Errorf("at most 20 SKUs")
	}
	if skuCSV == "" && len(strings.TrimSpace(family)) < 4 {
		return r, fmt.Errorf("family phrase must contain at least four characters")
	}
	selected := map[string]map[string]bool{}
	refreshed := map[string]bool{}
	for _, src := range sources {
		path := filepath.Join(root, src.ID, "offers.jsonl")
		file, err := os.Open(path)
		if err != nil {
			return r, err
		}
		scan := bufio.NewScanner(file)
		scan.Buffer(make([]byte, 64<<10), 4<<20)
		selected[src.ID] = map[string]bool{}
		for scan.Scan() {
			var offer collector.Offer
			if err := json.Unmarshal(scan.Bytes(), &offer); err != nil {
				file.Close()
				return r, fmt.Errorf("%s: %w", path, err)
			}
			sku := strings.ToUpper(strings.TrimSpace(offer.SKU))
			if offer.Source != src.ID || sku == "" || !collector.Allowed(src, offer.URL) {
				continue
			}
			if skuCSV == "" && strings.Contains(strings.ToLower(offer.Name), strings.ToLower(strings.TrimSpace(family))) {
				wanted[sku] = true
			}
			selected[src.ID][offer.URL+"\x00"+sku] = true
		}
		if err := scan.Err(); err != nil {
			file.Close()
			return r, err
		}
		file.Close()
	}
	if len(wanted) == 0 || len(wanted) > 20 {
		return r, fmt.Errorf("feed matched no SKUs or more than 20; narrow the family")
	}
	for sku := range wanted {
		r.RequestedSKUs = append(r.RequestedSKUs, sku)
	}
	sort.Strings(r.RequestedSKUs)
	if includeCompetitors {
		for _, profile := range collector.CompetitorProfiles() {
			selected[profile.ID] = map[string]bool{}
			for _, url := range profile.Seeds {
				for _, sku := range r.RequestedSKUs {
					selected[profile.ID][url+"\x00"+sku] = true
				}
			}
			sources = append(sources, profile)
		}
	}
	// Revisit only URLs already tied to selected SKUs. Round-robin source
	// ordering lets a small page budget reach multiple sellers.
	queues := map[string][]string{}
	pageSKUs := map[string]map[string][]string{}
	for _, src := range sources {
		pageSKUs[src.ID] = map[string][]string{}
		r.Coverage[src.ID] = map[string]string{}
		for _, sku := range r.RequestedSKUs {
			r.Coverage[src.ID][sku] = "not_in_existing_feed"
			if src.ID == "allsoft" || src.ID == "syssoft" {
				r.Coverage[src.ID][sku] = "not_on_seed_page"
			}
		}
		urls := map[string]bool{}
		for pair := range selected[src.ID] {
			url, sku, _ := strings.Cut(pair, "\x00")
			if wanted[sku] {
				urls[url] = true
				pageSKUs[src.ID][url] = append(pageSKUs[src.ID][url], sku)
				r.Coverage[src.ID][sku] = "skipped_budget"
			}
		}
		ordered := make([]string, 0, len(urls))
		for url := range urls {
			ordered = append(ordered, url)
		}
		sort.Strings(ordered)
		queues[src.ID] = ordered
		r.CandidatePages += len(ordered)
	}
	bySource := map[string][]collector.Offer{}
	for index := 0; r.Pages < maxPages; index++ {
		remaining := false
		for _, src := range sources {
			if index >= len(queues[src.ID]) || r.Pages == maxPages {
				continue
			}
			remaining = true
			url := queues[src.ID][index]
			for _, sku := range pageSKUs[src.ID][url] {
				if r.Coverage[src.ID][sku] == "observed" {
					continue
				}
				r.Coverage[src.ID][sku] = "no_verified_price"
				if src.ID == "allsoft" || src.ID == "syssoft" {
					r.Coverage[src.ID][sku] = "not_on_seed_page"
				}
			}
			if err := ctx.Err(); err != nil {
				return r, err
			}
			r.Pages++
			body, err := fetch(ctx, src, url)
			if err != nil {
				r.Failures[url] = err.Error()
				for _, sku := range pageSKUs[src.ID][url] {
					if r.Coverage[src.ID][sku] != "observed" {
						r.Coverage[src.ID][sku] = "fetch_failed"
					}
				}
				continue
			}
			var rows []collector.Offer
			if src.ID == "softmagazin" {
				page, inspectErr := collector.InspectPage(src, url, bytes.NewReader(body), time.Now().UTC())
				rows, err = page.Offers, inspectErr
			} else {
				rows, err = collector.Extract(src, url, bytes.NewReader(body), time.Now().UTC())
			}
			if err != nil {
				r.Failures[url] = err.Error()
				for _, sku := range pageSKUs[src.ID][url] {
					if r.Coverage[src.ID][sku] != "observed" {
						r.Coverage[src.ID][sku] = "parse_failed"
					}
				}
				continue
			}
			for _, offer := range rows {
				if wanted[strings.ToUpper(offer.SKU)] && offer.PriceMinor > 0 && offer.Currency == "RUB" && collector.Allowed(src, offer.URL) {
					bySource[src.ID] = append(bySource[src.ID], offer)
					refreshed[strings.ToUpper(offer.SKU)] = true
					r.Coverage[src.ID][strings.ToUpper(offer.SKU)] = "observed"
				}
			}
		}
		if !remaining {
			break
		}
	}
	for _, src := range sources {
		offers := bySource[src.ID]
		if err := writeOffers(filepath.Join(out, src.ID, "offers.jsonl"), offers); err != nil {
			return r, err
		}
		r.Sources[src.ID] = len(offers)
		r.Offers += len(offers)
	}
	for _, sku := range r.RequestedSKUs {
		if !refreshed[sku] {
			r.Missing = append(r.Missing, sku)
		}
	}
	r.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	r.Status = "ok"
	if len(r.Missing) > 0 || len(r.Failures) > 0 || r.Pages < r.CandidatePages {
		r.Status = "partial"
	}
	for _, sourceCoverage := range r.Coverage {
		for _, status := range sourceCoverage {
			if status != "observed" {
				r.Status = "partial"
			}
		}
	}
	return r, nil
}

func writeOffers(path string, offers []collector.Offer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".offers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	for _, offer := range offers {
		if err := enc.Encode(offer); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
