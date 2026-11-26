// fullcatalog is a single-writer, resumable public catalog worker.
// Each successfully flushed JSON line is a restart checkpoint.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	catalog "agent/collector"
)

type inventory struct {
	Source       string   `json:"source"`
	DiscoveredAt string   `json:"discovered_at"`
	URLs         []string `json:"urls"`
	VariantURLs  []string `json:"variant_urls,omitempty"`
}
type progress struct {
	Source                 string         `json:"source"`
	State                  string         `json:"state"`
	UpdatedAt              string         `json:"updated_at"`
	InventoryAt            string         `json:"inventory_at"`
	CandidatePages         int            `json:"candidate_pages"`
	ProcessedPages         int            `json:"processed_pages"`
	PendingPages           int            `json:"pending_pages"`
	ListedPages            int            `json:"listing_only_pages"`
	States                 map[string]int `json:"page_states"`
	PricedOffers           int            `json:"priced_offers"`
	Variants               int            `json:"variants"`
	ExpectedVariantURLs    int            `json:"expected_variant_urls"`
	ObservedVariantURLs    int            `json:"observed_variant_urls"`
	CheckedVariantURLs     int            `json:"checked_variant_urls"`
	UnavailableVariantURLs int            `json:"unavailable_variant_urls"`
	UnresolvedVariantURLs  int            `json:"unresolved_variant_urls"`
	ExcludedVariantURLs    int            `json:"excluded_variant_urls"`
	PendingVariantURLs     int            `json:"pending_variant_urls"`
}

func writeAtomic(path string, fn func(io.Writer) error) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".part-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	bw := bufio.NewWriter(f)
	if e = fn(bw); e != nil {
		return e
	}
	if e = bw.Flush(); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func saveJSON(path string, data any) error {
	return writeAtomic(path, func(w io.Writer) error { return json.NewEncoder(w).Encode(data) })
}
func unique(urls []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(urls))
	for _, v := range urls {
		if !seen[v] {
			result = append(result, v)
			seen[v] = true
		}
	}
	return result
}
func readInventory(ctx context.Context, dir string, s catalog.Source, f catalog.Fetch, refresh time.Duration) (inventory, error) {
	var inv inventory
	path := filepath.Join(dir, "inventory.json")
	b, e := os.ReadFile(path)
	if e == nil {
		e = json.Unmarshal(b, &inv)
		if e == nil && (inv.Source != s.ID || len(inv.URLs) == 0) {
			e = errors.New("invalid saved inventory")
		}
		if e != nil {
			return inv, e
		}
		var last progress
		st, stErr := os.ReadFile(filepath.Join(dir, "progress.json"))
		if stErr == nil {
			if e = json.Unmarshal(st, &last); e != nil {
				return inv, e
			}
		}
		start, e := time.Parse(time.RFC3339, inv.DiscoveredAt)
		if e != nil {
			return inv, e
		}
		closed := last.InventoryAt == inv.DiscoveredAt && (last.State == "complete" || last.State == "needs_review")
		if refresh == 0 || !closed || time.Since(start) < refresh {
			return inv, nil
		}
		// A new inventory is published only after discovery succeeds.
		inv = inventory{}
	} else if !errors.Is(e, os.ErrNotExist) {
		return inv, e
	}
	inv.Source = s.ID
	inv.DiscoveredAt = time.Now().UTC().Format(time.RFC3339)
	// A SoftMap family page contains all visible child offers. Keep the listed
	// child URLs to audit this grouping against the publisher's own sitemap.
	if s.ID == "softmap" {
		b, e = f(ctx, s, s.Sitemap)
		if e != nil {
			return inv, e
		}
		inv.URLs, _, e = catalog.ParseSitemap(s, bytes.NewReader(b))
		if e != nil {
			return inv, e
		}
		ss := s
		ss.PageInclude = `^https://softmap\.ru/[^/]+/[^/]+/element-[^/]+/$`
		inv.VariantURLs, _, e = catalog.ParseSitemap(ss, bytes.NewReader(b))
		if e != nil {
			return inv, e
		}
	} else {
		inv.URLs, e = catalog.Discover(ctx, s, f)
		if e != nil {
			return inv, e
		}
	}
	if len(inv.URLs) == 0 {
		return inv, errors.New("empty sitemap inventory")
	}
	// Keep smoke-test SKUs first to provide comparable quotes while processing.
	inv.URLs = unique(append(append([]string{}, s.Seeds...), inv.URLs...))
	inv.VariantURLs = unique(inv.VariantURLs)
	return inv, saveJSON(path, inv)
}
func replay(f *os.File) (map[string]catalog.PageResult, error) {
	byURL := map[string]catalog.PageResult{}
	buf := bufio.NewReader(f)
	var valid int64
	for {
		b, e := buf.ReadBytes('\n')
		if errors.Is(e, io.EOF) {
			// A power loss can leave only the last entry incomplete.
			if len(b) > 0 {
				if e = f.Truncate(valid); e != nil {
					return nil, e
				}
			}
			break
		}
		if e != nil {
			return nil, e
		}
		var p catalog.PageResult
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, fmt.Errorf("journal byte %d: %w", valid, e)
		}
		if p.URL == "" || p.State == "" {
			return nil, fmt.Errorf("invalid journal byte %d", valid)
		}
		valid += int64(len(b))
		byURL[p.URL] = p
	}
	_, e := f.Seek(0, io.SeekEnd)
	return byURL, e
}
func snapshot(dir string, inv inventory, pages map[string]catalog.PageResult) (progress, error) {
	status := progress{Source: inv.Source, State: "running", UpdatedAt: time.Now().UTC().Format(time.RFC3339), InventoryAt: inv.DiscoveredAt, CandidatePages: len(inv.URLs), States: map[string]int{}, ExpectedVariantURLs: len(inv.VariantURLs)}
	// Keep independent license variants and quantity tiers, even when a vendor reuses its SKU.
	type key struct {
		url, sku           string
		qty, maxQty, price int64
	}
	seen := map[key]bool{}
	observed := map[string]bool{}
	excluded := map[string]string{}
	var offers []catalog.Offer
	appendOffers := func(p catalog.PageResult) {
		if inv.Source == "softmagazin" && p.Category != "software" {
			if p.Category == "hardware" {
				excluded[p.URL] = p.At
				for _, url := range p.ExcludedURLs {
					excluded[url] = p.At
				}
			}
			return
		}
		for _, o := range p.Offers {
			k := key{url: o.URL, sku: o.SKU, price: o.PriceMinor}
			if o.Quantity != nil {
				k.qty = *o.Quantity
			}
			if o.MaxQuantity != nil {
				k.maxQty = *o.MaxQuantity
			}
			if !seen[k] {
				seen[k] = true
				offers = append(offers, o)
			}
		}
	}
	for _, u := range inv.URLs {
		p, ok := pages[u]
		if !ok {
			continue
		}
		if p.At >= inv.DiscoveredAt {
			if inv.Source == "softmagazin" && p.Category == "" && (p.State == "priced" || p.State == "no_public_price" || p.State == "unclassified") {
				// Legacy rows will be reclassified from the publisher's HTML.
			} else if p.EvidenceURL == "" {
				status.ProcessedPages++
				status.States[p.State]++
			} else if p.Category == "software" {
				status.ListedPages++
			}
			if p.State != "error" {
				for _, v := range p.Variants {
					observed[v.URL] = true
					status.Variants++
				}
			}
		}
		appendOffers(p)
	}
	for _, u := range inv.VariantURLs {
		if observed[u] {
			status.ObservedVariantURLs++
			continue
		}
		detail, ok := pages[u]
		if !ok {
			status.PendingVariantURLs++
			continue
		}
		appendOffers(detail)
		if detail.At < inv.DiscoveredAt {
			status.PendingVariantURLs++
			continue
		}
		status.CheckedVariantURLs++
		switch detail.State {
		case "priced", "no_public_price":
			status.ObservedVariantURLs++
			status.Variants += len(detail.Variants)
		case "unavailable":
			status.UnavailableVariantURLs++
		case "robots_excluded", "access_denied":
			status.ExcludedVariantURLs++
		default:
			status.UnresolvedVariantURLs++
		}
	}
	status.PendingPages = status.CandidatePages - status.ProcessedPages
	status.PricedOffers = len(offers)
	if status.PendingPages == 0 && status.PendingVariantURLs == 0 {
		status.State = "complete"
		if status.States["error"] > 0 || status.States["unclassified"] > 0 || status.States["robots_excluded"] > 0 || status.States["access_denied"] > 0 || status.ExcludedVariantURLs > 0 || status.UnresolvedVariantURLs > 0 {
			status.State = "needs_review"
		}
	}
	sort.SliceStable(offers, func(i, j int) bool {
		if offers[i].SKU != offers[j].SKU {
			return offers[i].SKU < offers[j].SKU
		}
		return offers[i].URL < offers[j].URL
	})
	if err := writeAtomic(filepath.Join(dir, "offers.jsonl"), func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, o := range offers {
			if e := enc.Encode(o); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return status, err
	}
	if err := writeAtomic(filepath.Join(dir, "offers.csv"), func(w io.Writer) error {
		if _, e := w.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
			return e
		}
		return catalog.CSV(w, offers)
	}); err != nil {
		return status, err
	}
	if inv.Source == "softmagazin" {
		if err := writeAtomic(filepath.Join(dir, "excluded.jsonl"), func(w io.Writer) error {
			urls := make([]string, 0, len(excluded))
			for u := range excluded {
				urls = append(urls, u)
			}
			sort.Strings(urls)
			enc := json.NewEncoder(w)
			for _, u := range urls {
				if e := enc.Encode(struct {
					URL string `json:"url"`
					At  string `json:"at"`
				}{u, excluded[u]}); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			return status, err
		}
	}
	return status, saveJSON(filepath.Join(dir, "progress.json"), status)
}

type outcome struct {
	p     catalog.PageResult
	fatal error
}

func readPage(ctx context.Context, s catalog.Source, u string, fetch catalog.Fetch) (catalog.PageResult, error) {
	at := time.Now().UTC()
	p := catalog.PageResult{URL: u, At: at.Format(time.RFC3339), State: "error"}
	b, err := fetch(ctx, s, u)
	if err == nil {
		p, err = catalog.InspectPage(s, u, bytes.NewReader(b), at)
		if s.ID == "softmagazin" && p.Category == "unknown" && (p.State == "priced" || p.State == "no_public_price") {
			p.State = "unclassified"
		}
	}
	if ctx.Err() != nil {
		return p, ctx.Err()
	}
	if err == nil {
		return p, nil
	}
	p.Error = err.Error()
	var he *catalog.HTTPStatus
	if errors.As(err, &he) {
		switch he.Code {
		case http.StatusNotFound, http.StatusGone:
			p.State = "unavailable"
		case http.StatusTooManyRequests, http.StatusForbidden, http.StatusServiceUnavailable:
			if he.Code == http.StatusForbidden && he.URL == u {
				p.State = "access_denied"
			}
			return p, fmt.Errorf("%s: paused after %w on %s (Retry-After %s)", s.ID, he, u, he.RetryAfter)
		}
	}
	if strings.Contains(err.Error(), "robots.txt disallows") {
		p.State = "robots_excluded"
	}
	return p, nil
}
func process(ctx context.Context, s catalog.Source, pending []string, fetch catalog.Fetch, consume func(catalog.PageResult) error) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	tasks := make(chan string)
	results := make(chan outcome)
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range tasks {
				p, fatal := readPage(workCtx, s, u, fetch)
				select {
				case results <- outcome{p, fatal}:
				case <-workCtx.Done():
					return
				}
				if fatal != nil {
					return
				}
			}
		}()
	}
	go func() {
		defer close(tasks)
		for _, u := range pending {
			select {
			case tasks <- u:
			case <-workCtx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	for result := range results {
		if result.fatal != nil {
			if result.p.State == "access_denied" {
				if err := consume(result.p); err != nil {
					return err
				}
			}
			return result.fatal
		}
		if err := consume(result.p); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func worker(ctx context.Context, s catalog.Source, root string, fetch catalog.Fetch, refresh time.Duration) (workerErr error) {
	dir := filepath.Join(root, s.ID)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("%s: active writer: %w", s.ID, e)
	}
	pauseFile := filepath.Join(dir, "retry-after.json")
	if raw, err := os.ReadFile(pauseFile); err == nil {
		var until time.Time
		if err = json.Unmarshal(raw, &until); err != nil {
			return err
		}
		if until.After(time.Now()) {
			return fmt.Errorf("%s: scheduled pause until %s", s.ID, until)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	defer func() {
		var status *catalog.HTTPStatus
		if errors.As(workerErr, &status) && status.RetryAfter.After(time.Now()) {
			if err := saveJSON(pauseFile, status.RetryAfter); err != nil {
				workerErr = errors.Join(workerErr, err)
			}
		}
	}()
	inv, e := readInventory(ctx, dir, s, fetch, refresh)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(dir, "pages.jsonl"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	pages, e := replay(f)
	if e != nil {
		return e
	}
	if _, e = snapshot(dir, inv, pages); e != nil {
		return e
	}
	defer func() {
		if _, err := snapshot(dir, inv, pages); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	enc := json.NewEncoder(f)
	since := 0
	lastExport := time.Now()
	consume := func(p catalog.PageResult) error {
		u := p.URL
		if p.State == "error" {
			p.Attempts = 1
			if old, ok := pages[u]; ok && old.State == "error" && old.At >= inv.DiscoveredAt {
				p.Attempts = max(1, old.Attempts) + 1
			}
		}
		if p.State == "error" && len(p.Offers) == 0 {
			p.Offers = pages[u].Offers
			p.Variants = pages[u].Variants
			p.Category = pages[u].Category
		}
		if p.Category == "hardware" {
			for _, o := range pages[u].Offers {
				p.ExcludedURLs = append(p.ExcludedURLs, o.URL)
			}
			p.ExcludedURLs = unique(append(p.ExcludedURLs, pages[u].ExcludedURLs...))
		}
		if e = enc.Encode(p); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		pages[u] = p
		since++
		if since == 1 && len(pages) == 1 || since >= 30 || time.Since(lastExport) > time.Minute {
			st, e := snapshot(dir, inv, pages)
			if e != nil {
				return e
			}
			fmt.Printf("%s %s %d/%d pages, %d prices\n", st.UpdatedAt, s.ID, st.ProcessedPages, st.CandidatePages, st.PricedOffers)
			since = 0
			lastExport = time.Now()
		}
		return nil
	}
	if s.ID == "softmagazin" {
		for _, u := range s.Seeds {
			if p, ok := pages[u]; ok && p.Category == "software" && p.At >= inv.DiscoveredAt {
				continue
			}
			if err := process(ctx, s, []string{u}, fetch, consume); err != nil {
				return err
			}
		}
		if _, err := snapshot(dir, inv, pages); err != nil {
			return err
		}
		if e = collectListings(ctx, s, dir, &inv, pages, fetch, consume); e != nil {
			return e
		}
	}
	var pending []string
	for _, u := range inv.URLs {
		p, ok := pages[u]
		if !ok || p.At < inv.DiscoveredAt || s.ID == "softmagazin" && p.Category == "" && (p.State == "priced" || p.State == "no_public_price" || p.State == "unclassified") || p.EvidenceURL != "" || p.State == "error" && p.Attempts < 3 || p.State == "unclassified" {
			pending = append(pending, u)
		}
	}
	if e = process(ctx, s, pending, fetch, consume); e != nil {
		return e
	}
	// A variant absent from the family DOM may still have a live standalone
	// page. Read only those missing variants and keep HTTP 404/410 as evidence.
	if len(inv.VariantURLs) > 0 {
		observed := map[string]bool{}
		for _, u := range inv.URLs {
			p := pages[u]
			if p.At >= inv.DiscoveredAt && p.State != "error" {
				for _, v := range p.Variants {
					observed[v.URL] = true
				}
			}
		}
		var gaps []string
		for _, u := range inv.VariantURLs {
			p, ok := pages[u]
			if !observed[u] && (!ok || p.At < inv.DiscoveredAt || p.State == "error" && p.Attempts < 3 || p.State == "unclassified") {
				gaps = append(gaps, u)
			}
		}
		if e = process(ctx, s, gaps, fetch, consume); e != nil {
			return e
		}
	}
	// Transient transport failures get a second opportunity in the same
	// generation. Control responses (403, 429, 503 and Retry-After) stop a run.
	candidates := append(append([]string{}, inv.URLs...), inv.VariantURLs...)
	for attempt := 1; attempt < 3; attempt++ {
		var failed []string
		for _, u := range candidates {
			p := pages[u]
			if p.State == "error" && p.At >= inv.DiscoveredAt && p.Attempts < 3 {
				failed = append(failed, u)
			}
		}
		if len(failed) == 0 {
			break
		}
		if e = process(ctx, s, failed, fetch, consume); e != nil {
			return e
		}
	}
	st, e := snapshot(dir, inv, pages)
	if e != nil {
		return e
	}
	fmt.Printf("%s %s %d/%d pages, %d prices, states %v\n", s.ID, st.State, st.ProcessedPages, st.CandidatePages, st.PricedOffers, st.States)
	return nil
}
func main() {
	root := flag.String("out", "", "persistent catalog directory")
	selected := flag.String("source", "", "one source ID, or comma-separated IDs")
	refresh := flag.Duration("refresh-after", 0, "discover a new generation when the previous scan has closed and is this old")
	flag.Parse()
	if *root == "" || *selected == "" {
		flag.Usage()
		os.Exit(2)
	}
	sources, e := catalog.Sources()
	if e != nil {
		panic(e)
	}
	ids := map[string]bool{}
	for _, v := range strings.Split(*selected, ",") {
		ids[v] = true
	}
	var chosen []catalog.Source
	for _, s := range sources {
		if ids[s.ID] {
			chosen = append(chosen, s)
			delete(ids, s.ID)
		}
	}
	if len(ids) > 0 {
		fmt.Fprintln(os.Stderr, "unknown sources", ids)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpClient := catalog.NewHTTP()
	res := make(chan error, len(chosen))
	for _, s := range chosen {
		go func() { res <- worker(ctx, s, *root, httpClient.Fetch, *refresh) }()
	}
	failed := false
	for range chosen {
		if e := <-res; e != nil {
			fmt.Fprintln(os.Stderr, e)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
