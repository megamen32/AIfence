package catalog

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Opt-in, read-only replay against the private local archive. Never fetch
// a website and never modify the collection ledger.
func TestArchivedSoftMagazinDisposition(t *testing.T) {
	root := os.Getenv("MARKET_ARCHIVE")
	if root == "" {
		t.Skip("private archive not supplied")
	}
	db, e := sql.Open("sqlite", "file:"+filepath.Join(root, "crawl.sqlite")+"?mode=ro&_pragma=busy_timeout(10000)")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	j := &Journal{db: db, root: root}
	rows, e := db.Query(`SELECT r.id FROM repair_batch_examples x JOIN repair_batches b ON b.id=x.batch_id JOIN http_responses r ON r.id=x.evidence_id
 WHERE b.source='softmagazin' ORDER BY b.created_at,r.id LIMIT 20`)
	if e != nil {
		t.Fatal(e)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 20 {
		t.Fatalf("expected first historical batch, got %d", len(ids))
	}
	s, _ := sourceByID("softmagazin")
	for _, id := range ids {
		ev, e := j.Evidence(id)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := j.Raw(ev)
		if e != nil {
			t.Fatal(e)
		}
		p, e := InspectPage(s, ev.URL, bytes.NewReader(raw), ev.At)
		if e != nil {
			t.Fatal(e)
		}
		if p.State != "unavailable" || len(p.Offers) != 0 || p.StatusEvidence != "exact_product_discontinued_without_public_quote" {
			t.Fatalf("id=%d state=%s offers=%d evidence=%s", id, p.State, len(p.Offers), p.StatusEvidence)
		}
	}
	t.Log("original rejected batch: 20/20 exact discontinued cards confirmed by code, zero invented quotes")
	rows, e = db.Query("SELECT id FROM http_responses WHERE source='softmagazin' AND status=200 ORDER BY id")
	if e != nil {
		t.Fatal(e)
	}
	ids = nil
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	rows.Close()
	states := map[string]int{}
	checked, priorPriced, regression := 0, 0, 0
	for _, id := range ids {
		ev, e := j.Evidence(id)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := j.Raw(ev)
		if e != nil {
			t.Fatal(e)
		}
		if !isHTML(raw) {
			continue
		}
		doc, e := goquery.NewDocumentFromReader(bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		before, e := inspectDocumentBase(s, ev.URL, doc, ev.At)
		if e != nil {
			continue
		}
		checked++
		after := before
		if classifySoftMagazinDisposition(ev.URL, doc, &after) {
			states[after.State]++
		}
		if len(before.Offers) > 0 {
			priorPriced++
			if after.State != before.State || len(after.Offers) != len(before.Offers) {
				regression++
			}
			for i, v := range before.Offers {
				if after.Offers[i].SKU != v.SKU || after.Offers[i].URL != v.URL || after.Offers[i].PriceMinor != v.PriceMinor {
					regression++
				}
			}
		}
	}
	if regression != 0 {
		t.Fatalf("regressions=%d", regression)
	}
	t.Logf("actual archive: %d parsed responses, %d with existing quotes, new statuses=%v, price regressions=%d", checked, priorPriced, states, regression)
}

func TestCodePreflightRequeuesOnlyProvenArchive(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	u := "https://www.softmagazin.ru/1c/example-license/"
	raw := []byte(`<html><head><link rel="canonical" href="https://www.softmagazin.ru/1c/example-license/"><script type="application/ld+json">{"@type":"Product","@id":"https://www.softmagazin.ru/1c/example-license/#product","sku":"1C-EX","name":"1C license","offers":{"@type":"Offer","description":"Цена по запросу","priceCurrency":"RUB"}}</script></head><body><div class="info-category"><div class="base-links-tovar-item"><a href="/soft/1c/">Software</a></div></div><div class="offer-tovar-style"><h1>1C license</h1><div class="articul-manufacturer"><h2>Артикул: <span>1C-EX</span></h2></div><span class="out-of-production-label">Снято с производства</span></div></body></html>`)
	at := time.Now().UTC()
	ev, e := j.Archive("softmagazin", u, u, 200, "", at, raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = j.RecordFailure("softmagazin", u, "g", "page", "parse_unclassified", "", ev.ID, at, time.Time{}); e != nil {
		t.Fatal(e)
	}
	b := RepairBatch{ID: "old-decision", Source: "softmagazin", ErrorType: "parse_unclassified", Examples: []RepairExample{{EvidenceID: ev.ID, URL: u, CapturedAt: at}}}
	if e = j.MarkBatch(b, "rejected", "model found no safe selector repair"); e != nil {
		t.Fatal(e)
	}
	report, e := j.PreflightRepairs(100)
	if e != nil || report.Checked != 1 || report.Requeued != 1 {
		t.Fatal(report, e)
	}
	f, e := j.Failure("softmagazin", u, "g", "page")
	if e != nil || f.State != "retry" || f.Attempts != 0 || f.EvidenceID != ev.ID {
		t.Fatal(f, e)
	}
	body, stored, ok, e := j.RetryBody("softmagazin", u, "g", "page")
	if e != nil || !ok || stored.ID != ev.ID || !bytes.Equal(body, raw) {
		t.Fatal("must retry the exact local archive", e)
	}
	if report, e = j.PreflightRepairs(100); e != nil || report.Checked != 0 || report.Requeued != 0 {
		t.Fatal(report, e)
	}
}

func TestSoftlineRevisionReopensArchiveOnce(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	u := "https://store.softline.ru/vendor/license-123/"
	at := time.Now().UTC()
	ev, e := j.Archive("softline", u, u, 200, "", at, []byte("<html>family</html>"))
	if e != nil {
		t.Fatal(e)
	}
	if e = j.RecordFailure("softline", u, "g", "product", "parse_unclassified", "", ev.ID, at, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if n, e := j.RequeueSoftlineVariantChecks("g"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	f, e := j.Failure("softline", u, "g", "product")
	if e != nil || f.Attempts != 0 || f.State != "retry" || f.EvidenceID != ev.ID {
		t.Fatal(f, e)
	}
	if n, e := j.RequeueSoftlineVariantChecks("g"); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e = j.RecordFailure("softline", u, "g", "product", "source_variant_not_listed", "", ev.ID, at, at.Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	f, e = j.Failure("softline", u, "g", "product")
	if e != nil || f.State != "retry" || f.ErrorType != "source_variant_not_listed" || f.NextAttempt.Before(at.Add(24*time.Hour)) {
		t.Fatal(f, e)
	}
	if n, e := j.RequeueSoftlineVariantChecks("g"); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
