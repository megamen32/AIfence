package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestJournalArchiveBackoffAndResume(t *testing.T) {
	root := t.TempDir()
	j, e := OpenJournal(root)
	if e != nil {
		t.Fatal(e)
	}
	url := "https://store.softline.ru/kaspersky/abc-7/"
	at := time.Now().UTC().Add(-time.Hour)
	raw := []byte("<html><h1>License</h1></html>")
	ev, e := j.Archive("softline", url, url, 200, "", at, raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = j.Seed("softline", "g", "product", []string{url}, DemoPriority); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= MaxAttempts; i++ {
		if e = j.RecordFailure("softline", url, "g", "product", "parse_unclassified", "", ev.ID, at, time.Time{}); e != nil {
			t.Fatal(e)
		}
		f, e := j.Failure("softline", url, "g", "product")
		if e != nil {
			t.Fatal(e)
		}
		if f.Attempts != i {
			t.Fatal(f)
		}
		if i < MaxAttempts {
			if !f.NextAttempt.Equal(at.Add(RetryDelay(i))) {
				t.Fatal("wrong backoff", f)
			}
			allowed, e := j.MayRun("softline", url, "g", "product", at)
			if e != nil || allowed {
				t.Fatal("retry too soon", e)
			}
		} else if f.State != "exhausted" {
			t.Fatal("fifth attempt did not exhaust", f)
		}
	}
	if e = j.Close(); e != nil {
		t.Fatal(e)
	}
	j, e = OpenJournal(root)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	b, stored, ok, e := j.RetryBody("softline", url, "g", "product")
	if e != nil || !ok || string(b) != string(raw) || !stored.At.Equal(at) {
		t.Fatal("archive lost or freshness changed", e)
	}
	if allowed, e := j.MayRun("softline", url, "g", "product", time.Now().Add(time.Hour)); e != nil || allowed {
		t.Fatal("exhausted task retried")
	}
	if e = j.Resolve("softline", url, "g", "product", ev.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = j.Failure("softline", url, "g", "product"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("resolved failure remained", e)
	}
	if allowed, e := j.MayRun("softline", url, "g", "product", time.Now()); e != nil || allowed {
		t.Fatal("resolved task fetched again")
	}
	path, e := j.archivePath(ev.Digest)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = j.Raw(ev); e == nil {
		t.Fatal("corrupt raw accepted")
	}
}
func TestRetryUsesArchiveNotNetwork(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	s := Source{ID: "softline", Host: "store.softline.ru"}
	u := "https://store.softline.ru/vendor/abc-7/"
	at := time.Now().UTC().Add(-time.Hour)
	ev, e := j.Archive(s.ID, u, u, 200, "", at, []byte("<h1>Product</h1>"))
	if e != nil {
		t.Fatal(e)
	}
	if e = j.RecordFailure(s.ID, u, "g", "product", "parse_unclassified", "", ev.ID, at, time.Time{}); e != nil {
		t.Fatal(e)
	}
	ctx := WithJournal(context.Background(), j, "g", "product")
	d := DownloadTask(ctx, s, u, func(context.Context, Source, string) ([]byte, error) {
		t.Fatal("parse retry touched network")
		return nil, nil
	})
	if d.Err != nil || d.Skip || d.Evidence.ID != ev.ID || !d.At.Equal(at) {
		t.Fatalf("retry %+v", d)
	}
}
func TestAdaptiveConcurrencyAndPacing(t *testing.T) {
	h := &hostState{capacity: 8, changed: make(chan struct{})}
	var mu sync.Mutex
	var starts []time.Time
	peak := 0
	active := 0
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := h.wait(context.Background(), 5*time.Millisecond); e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			h.release()
		}()
	}
	wg.Wait()
	if peak < 2 || peak > 8 {
		t.Fatalf("concurrency %d", peak)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 4*time.Millisecond {
			t.Fatal("pace violation")
		}
	}
	for range 256 {
		h.feedback(200, time.Time{}, "")
	}
	if h.capacity != 16 {
		t.Fatal("healthy capacity did not grow", h.capacity)
	}
	h.feedback(500, time.Time{}, "")
	if h.capacity != 8 || h.adaptiveDelay < 2*time.Second {
		t.Fatal("5xx did not reduce throughput")
	}
	h.feedback(429, time.Now().Add(time.Hour), "https://store.softline.ru/a")
	var paused *AdmissionPaused
	if e := h.wait(context.Background(), time.Second); !errors.As(e, &paused) {
		t.Fatal("admitted after 429", e)
	}
}
func TestStructuredExactProduct(t *testing.T) {
	s := Source{ID: "softline", Host: "store.softline.ru"}
	u := "https://store.softline.ru/vendor/abc-7/"
	body := fmt.Sprintf(`<link rel="canonical" href="%s"><script type="application/ld+json">{"@type":"Product","name":"License","sku":"ABC","url":"%s","offers":{"@type":"Offer","url":"%s","price":"123.45","priceCurrency":"RUB"}}</script>`, u, u, u)
	doc, e := goquery.NewDocumentFromReader(strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	o := StructuredOffer(s, u, doc, time.Now())
	if o == nil || o.PriceMinor != 12345 || o.SKU != "ABC" || o.PriceBasis != "displayed_total" {
		t.Fatal(o)
	}
	bad := strings.Replace(body, `"@type":"Offer"`, `"@type":"AggregateOffer"`, 1)
	doc, _ = goquery.NewDocumentFromReader(strings.NewReader(bad))
	if StructuredOffer(s, u, doc, time.Now()) != nil {
		t.Fatal("aggregate price accepted")
	}
}
func TestRepairBatchGateAndHeldOut(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	at := time.Now().UTC().Add(-time.Hour)
	var urls []string
	makeBody := func(u string, id int, price string, baseline bool) []byte {
		config := fmt.Sprintf(`{"product":{"id":%d,"type":"software","minQuantity":1}}`, id)
		if baseline {
			config = fmt.Sprintf(`{"product":{"id":%d,"type":"software","minQuantity":1,"priceWithDiscount":"200"}}`, id)
		}
		return []byte(fmt.Sprintf(`<!doctype html><html><head><link rel="canonical" href="%s"></head><body><script id="js-front-config">window.temporaryFrontConfig = %s</script><main><h1>License</h1><b class="js-sku-btn" data-code="ABC">ABC</b><i class="new-price">%s</i><meta class="backup-price" content="%s"><i class="currency">RUB</i></main></body></html>`, u, config, price, price))
	}
	for i := 1; i <= 12; i++ {
		u := fmt.Sprintf("https://store.softline.ru/vendor/abc-%d/", i)
		urls = append(urls, u)
		ev, e := j.Archive("softline", u, u, 200, "", at, makeBody(u, i, "123", false))
		if e != nil {
			t.Fatal(e)
		}
		for range 5 {
			if e = j.RecordFailure("softline", u, "g", "product", "parse_unclassified", "", ev.ID, at, time.Time{}); e != nil {
				t.Fatal(e)
			}
		}
	}
	b, e := j.NextRepairBatch(time.Now())
	if e != nil || b == nil || len(b.Examples) < 10 || len(b.Examples) > 20 {
		t.Fatalf("batch %+v %v", b, e)
	}
	proposal := RuleProposal{Source: "softline", Reason: "two explicit price fields", Container: "main", Name: Field{Selector: "h1"}, SKU: Field{Selector: ".js-sku-btn", Attr: "data-code"}, Price: Field{Selector: ".new-price"}, ConfirmPrice: Field{Selector: ".backup-price", Attr: "content"}, Currency: Field{Selector: ".currency"}}
	report, e := j.ValidateRepair(*b, proposal)
	if e != nil || !report.Accepted || report.Improvements != 12 || report.HeldOutImprovements < 1 || report.Regressions != 0 {
		t.Fatalf("gate %+v %v", report, e)
	}
	if e = j.ActivateRepair(*b, proposal, report); e != nil {
		t.Fatal(e)
	}
	f, e := j.Failure("softline", urls[0], "g", "product")
	if e != nil || f.State != "retry" || f.Attempts != 0 {
		t.Fatal(f, e)
	}
	raw, ev, ok, e := j.RetryBody("softline", urls[0], "g", "product")
	if e != nil || !ok {
		t.Fatal(e)
	}
	s, _ := sourceByID("softline")
	o, e := j.RepairOffer(s, urls[0], raw, ev.At)
	if e != nil || o == nil || o.PriceMinor != 12300 {
		t.Fatal(o, e)
	}
	known := "https://store.softline.ru/vendor/abc-999/"
	if _, e = j.Archive("softline", known, known, 200, "", at, makeBody(known, 999, "999", true)); e != nil {
		t.Fatal(e)
	}
	rejected, e := j.ValidateRepair(*b, proposal)
	if e != nil || rejected.Accepted || rejected.Regressions == 0 {
		t.Fatalf("regression not rejected %+v %v", rejected, e)
	}
	if _, e = os.Stat(filepath.Join(j.root, "crawl.sqlite")); e != nil {
		t.Fatal(e)
	}
}
