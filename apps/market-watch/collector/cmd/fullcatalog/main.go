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
	"syscall"
	"time"

	catalog "agent/collector"
)

type inventory struct {
	Source           string   `json:"source"`
	DiscoveredAt     string   `json:"discovered_at"`
	URLs             []string `json:"urls"`
	VariantSitemapAt string   `json:"variant_sitemap_at,omitempty"`
	VariantURLs      []string `json:"variant_urls,omitempty"`
}
type progress struct {
	Source                  string         `json:"source"`
	CategoryDiscovery       *bool          `json:"category_discovery_complete,omitempty"`
	State                   string         `json:"state"`
	UpdatedAt               string         `json:"updated_at"`
	InventoryAt             string         `json:"inventory_at"`
	CandidatePages          int            `json:"candidate_pages"`
	ProcessedPages          int            `json:"processed_pages"`
	PendingPages            int            `json:"pending_pages"`
	ListedPages             int            `json:"listing_only_pages"`
	States                  map[string]int `json:"page_states"`
	PricedOffers            int            `json:"priced_offers"`
	Variants                int            `json:"variants"`
	ExpectedVariantURLs     int            `json:"expected_variant_urls"`
	ObservedVariantURLs     int            `json:"observed_variant_urls"`
	CheckedVariantURLs      int            `json:"checked_variant_urls"`
	UnavailableVariantURLs  int            `json:"unavailable_variant_urls"`
	UnresolvedVariantURLs   int            `json:"unresolved_variant_urls"`
	ExcludedVariantURLs     int            `json:"excluded_variant_urls"`
	SoftwareVariantURLs     int            `json:"software_variant_urls,omitempty"`
	HardwareVariantURLs     int            `json:"hardware_variant_urls,omitempty"`
	UnknownScopeVariantURLs int            `json:"unknown_scope_variant_urls,omitempty"`
	VariantSitemapAt        string         `json:"variant_sitemap_at,omitempty"`
	PendingVariantURLs      int            `json:"pending_variant_urls"`
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
		if j, _, _ := catalog.JobJournal(ctx); j != nil && closed {
			_, pending, err := j.NextDue(s.ID, inv.DiscoveredAt, "page")
			if err != nil {
				return inv, err
			}
			if pending {
				closed = false
			}
		}
		if refresh == 0 || !closed || time.Since(start) < refresh {
			if s.ID == "softmagazin" && (len(inv.VariantURLs) == 0 || inv.VariantSitemapAt == "") {
				if e = discoverSoftMagazinVariants(ctx, s, f, &inv); e != nil {
					return inv, e
				}
				return inv, saveJSON(path, inv)
			}
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
	if s.ID == "softmagazin" {
		if e = discoverSoftMagazinVariants(ctx, s, f, &inv); e != nil {
			return inv, e
		}
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
			if p.State != "error" && (inv.Source != "softmagazin" || p.Category == "software") {
				for _, v := range p.Variants {
					observed[v.URL] = true
					status.Variants++
				}
			}
		}
		appendOffers(p)
	}
	if inv.Source == "softmagazin" {
		status.VariantSitemapAt = inv.VariantSitemapAt
		for _, row := range reconcileSoftMagazin(inv, pages) {
			switch row.Scope {
			case "software":
				status.SoftwareVariantURLs++
			case "hardware":
				status.HardwareVariantURLs++
			default:
				status.UnknownScopeVariantURLs++
			}
			if row.Direct {
				status.CheckedVariantURLs++
				appendOffers(pages[row.URL])
			}
			switch row.State {
			case "priced", "no_public_price":
				status.ObservedVariantURLs++
			case "out_of_scope": // Hardware is accounted, not an error.
			case "unavailable":
				status.UnavailableVariantURLs++
			case "access_denied", "robots_excluded":
				status.ExcludedVariantURLs++
			case "pending":
				status.PendingVariantURLs++
			default:
				status.UnresolvedVariantURLs++
			}
		}
	} else {
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
	}
	status.PendingPages = status.CandidatePages - status.ProcessedPages
	status.PricedOffers = len(offers)
	if inv.Source == "softmagazin" {
		value := false
		var audit categoryAudit
		if b, err := os.ReadFile(filepath.Join(dir, "category-audit.json")); err == nil {
			if err = json.Unmarshal(b, &audit); err != nil {
				return status, err
			}
			value = audit.InventoryAt == inv.DiscoveredAt && audit.Complete
		} else if !errors.Is(err, os.ErrNotExist) {
			return status, err
		}
		status.CategoryDiscovery = &value
	}
	if status.PendingPages == 0 && status.PendingVariantURLs == 0 {
		status.State = "complete"
		if status.CategoryDiscovery != nil && !*status.CategoryDiscovery || status.UnknownScopeVariantURLs > 0 || status.States["error"] > 0 || status.States["unclassified"] > 0 || status.States["robots_excluded"] > 0 || status.States["access_denied"] > 0 || status.ExcludedVariantURLs > 0 || status.UnresolvedVariantURLs > 0 {
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

func parseDownloaded(s catalog.Source, u string, d catalog.Downloaded) (catalog.PageResult, error, error) {
	at := d.At
	p := catalog.PageResult{URL: u, At: at.Format(time.RFC3339), State: "error"}
	err := d.Err
	if err == nil {
		p, err = catalog.InspectPage(s, u, bytes.NewReader(d.Body), at)
		if s.ID == "softmagazin" && p.Category == "unknown" && (p.State == "priced" || p.State == "no_public_price") {
			p.State = "unclassified"
		}
	}
	if err == nil {
		return p, nil, nil
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
			return p, err, fmt.Errorf("%s: paused after %w on %s", s.ID, he, u)
		}
	}
	if strings.Contains(err.Error(), "robots.txt disallows") {
		p.State = "robots_excluded"
	}
	if catalog.StopRequest(err) || errors.Is(err, context.Canceled) {
		return p, err, err
	}
	return p, err, nil
}

func readPage(ctx context.Context, s catalog.Source, u string, fetch catalog.Fetch) (catalog.PageResult, error) {
	p, _, e := parseDownloaded(s, u, catalog.DownloadTask(ctx, s, u, fetch))
	return p, e
}

type queueOutcome struct {
	p               catalog.PageResult
	d               catalog.Downloaded
	parseErr, fatal error
}

func processBatch(ctx context.Context, s catalog.Source, pending []string, fetch catalog.Fetch, consume func(catalog.PageResult) error) error {
	var controlErr error
	returnErr := catalog.Pipeline(ctx, pending, func(work context.Context, u string) catalog.Downloaded {
		return catalog.DownloadTask(work, s, u, fetch)
	}, func(u string, d catalog.Downloaded) queueOutcome {
		if d.Skip {
			return queueOutcome{d: d}
		}
		p, e, fatal := parseDownloaded(s, u, d)
		if e == nil && fatal == nil && p.State == "unclassified" && len(p.Offers) == 0 && (s.ID != "softmagazin" || p.Category == "software") {
			if j, _, _ := catalog.JobJournal(ctx); j != nil {
				o, err := j.RepairOffer(s, u, d.Body, d.At)
				if err != nil {
					return queueOutcome{p, d, err, err}
				}
				if o != nil {
					p.Offers = []catalog.Offer{*o}
					p.Variants = append(p.Variants, catalog.Variant{URL: o.URL, Name: o.Name, SKU: o.SKU, Status: "priced"})
					p.State = "priced"
				}
			}
		}
		return queueOutcome{p, d, e, fatal}
	}, func(r queueOutcome) error {
		if r.d.Skip {
			return nil
		}
		var admission *catalog.AdmissionPaused
		if errors.As(r.d.Err, &admission) || errors.Is(r.d.Err, context.Canceled) {
			return r.fatal
		}
		if r.fatal != nil {
			var old, new *catalog.HTTPStatus
			if errors.As(r.fatal, &new) && (!errors.As(controlErr, &old) || new.RetryAfter.After(old.RetryAfter)) {
				controlErr = r.fatal
			}
			if e := catalog.RecordTask(ctx, s, r.p.URL, r.d, false, r.parseErr, "network"); e != nil {
				return e
			}
			return r.fatal
		}
		if e := consume(r.p); e != nil {
			return e
		}
		resolved := r.p.State == "priced" || r.p.State == "no_public_price" || r.p.State == "not_product" || r.p.State == "unavailable" || r.p.State == "out_of_scope"
		typ := "parse_unclassified"
		if r.p.Category == "unknown" && s.ID == "softmagazin" {
			typ = "parse_unknown_scope"
		}
		if r.p.State == "robots_excluded" {
			typ = "robots_excluded"
		}
		return catalog.RecordTask(ctx, s, r.p.URL, r.d, resolved, r.parseErr, typ)
	})
	if controlErr != nil {
		return controlErr
	}
	return returnErr
}
func process(ctx context.Context, s catalog.Source, pending []string, fetch catalog.Fetch, consume func(catalog.PageResult) error) error {
	pending = unique(pending)
	catalog.PrioritizeURLs(pending)
	j, gen, kind := catalog.JobJournal(ctx)
	if j == nil {
		return processBatch(ctx, s, pending, fetch, consume)
	}
	if e := j.Seed(s.ID, gen, kind, pending, catalog.DemoPriority); e != nil {
		return e
	}
	for len(pending) > 0 {
		n := min(len(pending), 256)
		if e := processBatch(ctx, s, pending[:n], fetch, consume); e != nil {
			return e
		}
		pending = pending[n:]
		due, e := j.Due(s.ID, gen, kind, time.Now(), 128)
		if e != nil {
			return e
		}
		if len(due) > 0 {
			if e = processBatch(ctx, s, due, fetch, consume); e != nil {
				return e
			}
		}
	}
	return nil
}

func drainRetries(ctx context.Context, s catalog.Source, fetch catalog.Fetch, consume func(catalog.PageResult) error) error {
	j, gen, kind := catalog.JobJournal(ctx)
	if j == nil {
		return nil
	}
	for {
		due, e := j.Due(s.ID, gen, kind, time.Now(), 256)
		if e != nil {
			return e
		}
		if len(due) > 0 {
			if e = processBatch(ctx, s, due, fetch, consume); e != nil {
				return e
			}
			continue
		}
		next, ok, e := j.NextDue(s.ID, gen, kind)
		if e != nil || !ok {
			return e
		}
		delay := max(time.Second, time.Until(next))
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
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
		if errors.As(workerErr, &status) && catalog.FatalControl(workerErr) {
			until := status.RetryAfter
			if until.Before(time.Now().Add(3 * time.Hour)) {
				until = time.Now().Add(3 * time.Hour)
			}
			if err := saveJSON(pauseFile, until); err != nil {
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
	if journal, _, _ := catalog.JobJournal(ctx); journal != nil {
		ctx = catalog.WithJournal(ctx, journal, inv.DiscoveredAt, "page")
		for u, p := range pages {
			if p.At < inv.DiscoveredAt || p.EvidenceURL != "" || !(p.State == "unclassified" || p.State == "error") {
				continue
			}
			at, err := time.Parse(time.RFC3339, p.At)
			if err != nil {
				continue
			}
			typ, _ := catalog.FailureType(errors.New(p.Error), "parse_unclassified")
			if p.Category == "unknown" && s.ID == "softmagazin" {
				typ = "parse_unknown_scope"
			}
			id := int64(0)
			if ev, err := journal.Latest(s.ID, u); err == nil {
				id = ev.ID
			}
			if err = journal.SeedLegacyFailure(s.ID, u, inv.DiscoveredAt, "page", typ, at, id); err != nil {
				return err
			}
		}
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
		if !ok || p.At < inv.DiscoveredAt || s.ID == "softmagazin" && p.Category == "" && (p.State == "priced" || p.State == "no_public_price" || p.State == "unclassified") || p.EvidenceURL != "" || p.State == "error" && p.Attempts < catalog.MaxAttempts || p.State == "unclassified" {
			pending = append(pending, u)
		}
	}
	if e = process(ctx, s, pending, fetch, consume); e != nil {
		return e
	}
	if s.ID == "softmagazin" {
		var audit categoryAudit
		b, err := os.ReadFile(filepath.Join(dir, "category-audit.json"))
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &audit); err != nil {
			return err
		}
		if !audit.Complete {
			if e = collectListings(ctx, s, dir, &inv, pages, fetch, consume); e != nil {
				return e
			}
			var extra []string
			for _, u := range inv.URLs {
				p, ok := pages[u]
				if !ok || p.At < inv.DiscoveredAt || p.EvidenceURL != "" {
					extra = append(extra, u)
				}
			}
			if e = process(ctx, s, extra, fetch, consume); e != nil {
				return e
			}
		}
	}
	// A variant absent from the family DOM may still have a live standalone
	// page. Read only those missing variants and keep HTTP 404/410 as evidence.
	if len(inv.VariantURLs) > 0 && s.ID == "softmagazin" {
		var gaps []string
		for _, row := range reconcileSoftMagazin(inv, pages) {
			if row.State == "pending" || (row.State == "unclassified" && row.Direct) {
				gaps = append(gaps, row.URL)
			}
		}
		if e = process(ctx, s, gaps, fetch, consume); e != nil {
			return e
		}
	} else if len(inv.VariantURLs) > 0 {
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
			if !observed[u] && (!ok || p.At < inv.DiscoveredAt || p.State == "error" && p.Attempts < catalog.MaxAttempts || p.State == "unclassified") {
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
	if journal, _, _ := catalog.JobJournal(ctx); journal != nil {
		if e = drainRetries(ctx, s, fetch, consume); e != nil {
			return e
		}
	} else {
		for attempt := 1; attempt < 3; attempt++ {
			var failed []string
			for _, u := range candidates {
				p := pages[u]
				if p.State == "error" && p.At >= inv.DiscoveredAt && p.Attempts < catalog.MaxAttempts {
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
	journal, e := catalog.OpenJournal(*root)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer journal.Close()
	ctx = catalog.WithJournal(ctx, journal, "", "")
	httpClient := catalog.NewArchivedHTTP(journal)
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
