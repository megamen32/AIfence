package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newSurvey(t *testing.T, urls []string) *survey {
	t.Helper()
	root := t.TempDir()
	out := filepath.Join(root, "softline")
	if e := os.MkdirAll(out, 0700); e != nil {
		t.Fatal(e)
	}
	s := &survey{dir: out, out: out, known: map[string]bool{}, covered: map[string]softlinefamily.Product{}, prior: map[string]catalog.Offer{}, cfg: inventory{At: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), Products: urls}}
	for _, u := range urls {
		s.known[u] = true
	}
	return s
}

func TestBatchFirstExactURLsAndResume(t *testing.T) {
	a, b := "https://store.softline.ru/vendor/abc-7/", "https://store.softline.ru/vendor/def-8/"
	s := newSurvey(t, []string{a, b})
	at := time.Now().UTC().Truncate(time.Second)
	qty := int64(1)
	offer := catalog.Offer{Source: "softline", SKU: "ABC", Name: "A", URL: a, PriceMinor: 100, Currency: "RUB", Quantity: &qty, PriceBasis: "per_unit", CapturedAt: at.Format(time.RFC3339)}
	batch := verifiedBatch{EvidenceURL: "https://store.softline.ru/pricelists/", ObservedAt: at, Offers: []catalog.Offer{offer}}
	path := filepath.Join(t.TempDir(), "batch.json")
	if e := save(path, batch); e != nil {
		t.Fatal(e)
	}
	if e := s.loadVerifiedBatch(path); e != nil {
		t.Fatal(e)
	}
	count := 0
	fetcher := func(_ context.Context, _ catalog.Source, u string) ([]byte, error) {
		count++
		if u != b {
			t.Errorf("batch URL was requested: %s", u)
		}
		return nil, &catalog.HTTPStatus{Code: 404}
	}
	if e := s.scan(context.Background(), []string{a, b}, false, fetcher); e != nil {
		t.Fatal(e)
	}
	if count != 1 || s.covered[a].Offer == nil || s.covered[b].State != "unavailable" {
		t.Fatalf("coverage or requests: %d %#v", count, s.covered)
	}
	s.covered = map[string]softlinefamily.Product{}
	if e := s.loadVerifiedBatch(""); e != nil {
		t.Fatal(e)
	}
	if e := s.scan(context.Background(), []string{a, b}, false, fetcher); e != nil {
		t.Fatal(e)
	}
	if count != 1 || len(s.covered) != 2 {
		t.Fatal("resume repeated network request")
	}
	// Reject unverified price identities and inconsistent data before persisting.
	for _, o := range []catalog.Offer{
		{Source: "softline", SKU: "ABC", Name: "Other", URL: "https://store.softline.ru/vendor/other-9/", PriceMinor: 100, Currency: "RUB", Quantity: &qty, PriceBasis: "per_unit", CapturedAt: offer.CapturedAt},
		{Source: "softline", SKU: "ABC", Name: "A", URL: a, PriceMinor: 100, Currency: "RUB", PriceBasis: "displayed_total", CapturedAt: offer.CapturedAt},
	} {
		bad := batch
		bad.Offers = []catalog.Offer{o}
		if e := validateBatch(bad, s.known, time.Now().Add(-time.Hour), time.Now()); e == nil {
			t.Fatal("invalid feed accepted")
		}
	}
}

func TestPipelineAndResume(t *testing.T) {
	var urls []string
	for i := 0; i < 20; i++ {
		urls = append(urls, fmt.Sprintf("https://store.softline.ru/vendor/abc-%d/", i))
	}
	s := newSurvey(t, urls)
	var active, peak, calls atomic.Int32
	get := func(ctx context.Context, _ catalog.Source, _ string) ([]byte, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(15 * time.Millisecond):
		}
		return nil, &catalog.HTTPStatus{Code: 404}
	}
	if e := s.scan(context.Background(), urls, false, get); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 20 || peak.Load() < 2 || peak.Load() > scanWorkers || len(s.covered) != 20 {
		t.Fatalf("requests=%d peak=%d coverage=%d", calls.Load(), peak.Load(), len(s.covered))
	}
	s.covered = map[string]softlinefamily.Product{}
	if e := s.scan(context.Background(), urls, false, get); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 20 || len(s.covered) != 20 {
		t.Fatal("resumed network work")
	}
}

func TestRetainedEvidenceReparse(t *testing.T) {
	u := "https://store.softline.ru/vendor/abc-7/"
	s := newSurvey(t, []string{u})
	raw := []byte("<h1>Unpriced</h1>")
	n := 0
	get := func(context.Context, catalog.Source, string) ([]byte, error) { n++; return raw, nil }
	if e := s.scan(context.Background(), []string{u}, false, get); e != nil {
		t.Fatal(e)
	}
	path := checkpoint(filepath.Join(s.dir, "product"), u)
	var c check
	if e := read(path, &c); e != nil {
		t.Fatal(e)
	}
	if c.BodyHash == "" {
		t.Fatal("missing source evidence")
	}
	saved, e := readEvidence(evidencePath(path, c.BodyHash))
	if e != nil || string(saved) != string(raw) {
		t.Fatal("missing body", e)
	}
	at := c.At
	c.ParserVersion = parserVersion - 1
	if e := save(path, c); e != nil {
		t.Fatal(e)
	}
	s.covered = map[string]softlinefamily.Product{}
	if e := s.scan(context.Background(), []string{u}, false, get); e != nil {
		t.Fatal(e)
	}
	if e := read(path, &c); e != nil {
		t.Fatal(e)
	}
	if n != 1 || c.ParserVersion != parserVersion || !c.At.Equal(at) {
		t.Fatal("reparse fetched again or fabricated freshness")
	}
}

func TestPriorityOnlyReorders(t *testing.T) {
	a, b, c := "https://store.softline.ru/vendor/abc-7/", "https://store.softline.ru/vendor/def-8/", "https://store.softline.ru/vendor/ghi-9/"
	s := newSurvey(t, []string{a, b, c})
	for _, id := range []string{"softmagazin", "softmap"} {
		dir := filepath.Join(filepath.Dir(s.out), id)
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "offers.jsonl"), []byte(fmt.Sprintf("{\"source\":%q,\"sku\":\"DEF\",\"price_minor\":100,\"currency\":\"RUB\"}\n", id)), 0600); e != nil {
			t.Fatal(e)
		}
	}
	jobs := []pageJob{{a, "a"}, {b, "b"}, {c, "c"}}
	s.prioritizePending(jobs)
	if jobs[0].page != b || jobs[1].page != a || jobs[2].page != c || len(s.covered) != 0 {
		t.Fatal("priority lost URL or invented observation", jobs)
	}
}

func TestVerifiedBatchDoesNotOverwriteResolvedCheckpoint(t *testing.T) {
	u := "https://store.softline.ru/vendor/abc-7/"
	s := newSurvey(t, []string{u})
	qty := int64(1)
	at := time.Now().UTC().Truncate(time.Second)
	o := catalog.Offer{Source: "softline", SKU: "ABC", Name: "A", URL: u, PriceMinor: 100, Currency: "RUB", Quantity: &qty, PriceBasis: "per_unit", CapturedAt: at.Format(time.RFC3339)}
	b := verifiedBatch{EvidenceURL: "https://store.softline.ru/pricelists/", ObservedAt: at, Offers: []catalog.Offer{o}}
	path := filepath.Join(t.TempDir(), "batch.json")
	if e := save(path, b); e != nil {
		t.Fatal(e)
	}
	n := 0
	get := func(context.Context, catalog.Source, string) ([]byte, error) {
		n++
		return nil, &catalog.HTTPStatus{Code: 410}
	}
	if e := s.scan(context.Background(), []string{u}, false, get); e != nil {
		t.Fatal(e)
	}
	s.covered = map[string]softlinefamily.Product{}
	if e := s.loadVerifiedBatch(path); e != nil {
		t.Fatal(e)
	}
	if e := s.scan(context.Background(), []string{u}, false, get); e != nil {
		t.Fatal(e)
	}
	if n != 1 || s.covered[u].State != "unavailable" {
		t.Fatal("batch replaced resolved direct evidence")
	}
}

func TestPauseConcurrentPreservesLongestRetryAfter(t *testing.T) {
	s := newSurvey(t, []string{})
	now := time.Now()
	if e := s.recordPause(&catalog.HTTPStatus{Code: 429, RetryAfter: now.Add(9 * time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if e := s.recordPause(&catalog.HTTPStatus{Code: 503, RetryAfter: now.Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if resumeAllowed(s.out, now.Add(2*time.Hour)) == nil {
		t.Fatal("shorter retry overwrote longer pause")
	}
}
