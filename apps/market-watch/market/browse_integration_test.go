//go:build airlockintegration

package market

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalog "agent/collector"
	"agent/internal/db"
)

// The selected database gets transaction-local temporary tables only.
// Inputs are immutable local exports. There are no storefront requests.
func TestBrowseSavedCatalogSQL(t *testing.T) {
	dsn, dir := os.Getenv("MARKET_QA_PG"), os.Getenv("MARKET_QA_FILES")
	if dsn == "" || dir == "" {
		t.Skip("requires isolated PostgreSQL and saved catalog exports")
	}
	ctx := t.Context()
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	migration, err := os.ReadFile(filepath.Join(dir, "migration.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path TO pg_temp;"+strings.ReplaceAll(up, "CREATE TABLE", "CREATE TEMP TABLE")); err != nil {
		t.Fatal(err)
	}
	s := New(memoryStore{})
	s.queries = db.New(tx)
	sources, err := catalog.Sources()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		batch := make([]catalog.Offer, 0, 200)
		flush := func() {
			if err := s.storeOffers(ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
		visitExport(t, filepath.Join(dir, src.ID+".jsonl"), func(o catalog.Offer) {
			batch = append(batch, o)
			if len(batch) == cap(batch) {
				flush()
			}
		})
		flush()
	}
	status, err := s.Coverage(ctx, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Imported %d current offers", status.Total)
	for _, src := range status.Sources {
		t.Logf("%s: %d indexed offers", src.Source, src.Offers)
	}
	page := func(in ListInput) ([]Observation, bool) {
		t.Helper()
		ev, more, err := s.Browse(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return ev.Observations, more
	}
	for _, filter := range []string{"new", "renewal", "subscription", "perpetual", "unknown"} {
		in := ListInput{Source: "softmagazin", LicenseType: filter, Limit: 100}
		seen := map[string]bool{}
		pages := 0
		for {
			rows, more := page(in)
			pages++
			for _, row := range rows {
				if seen[row.ID] || row.Details.Seller != in.Source {
					t.Fatalf("duplicate ID or wrong source for %s", filter)
				}
				seen[row.ID] = true
				matched, anyMatch := false, false
				for _, rule := range licenseRules {
					if rule.re.MatchString(strings.ToLower(row.Details.Name)) {
						anyMatch = true
						if rule.id == filter {
							matched = true
						}
					}
				}
				if (filter == "unknown" && anyMatch) || (filter != "unknown" && !matched) {
					t.Fatalf("unrelated result for %s: %q", filter, row.Details.Name)
				}
			}
			if !more {
				break
			}
			if len(rows) != in.Limit || len(seen) > int(status.Total) {
				t.Fatalf("page not bounded (%s)", filter)
			}
			in.Offset += len(rows)
		}
		if len(seen) == 0 {
			t.Fatalf("missing expected fixture results for %s", filter)
		}
		t.Logf("SoftMagazin filter %s: %d offers, %d pages", filter, len(seen), pages)
	}
	// Traverse the unfiltered index too: count equality plus identity uniqueness
	// catches accidental skips and the phantom last page at exact multiples.
	seen := map[string]bool{}
	in := ListInput{Limit: 100}
	pages := 0
	for {
		rows, more := page(in)
		pages++
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatal("duplicate across SQL pages", r.ID)
			}
			seen[r.ID] = true
		}
		if !more {
			break
		}
		if len(rows) != in.Limit {
			t.Fatal("incomplete page with more=true")
		}
		in.Offset += len(rows)
	}
	if int64(len(seen)) != status.Total {
		t.Fatal("pagination mismatch", len(seen), status.Total)
	}
	t.Logf("All %d unique IDs traversed in %d pages", len(seen), pages)
	out, err := s.CompareSKU(ctx, CompareInput{SKU: strings.ToLower(checkSKU)})
	if err != nil || out.Truncated {
		t.Fatal("exact compare", out, err)
	}
	count, ranked := 0, 0
	for _, g := range out.Groups {
		if g.Comparable {
			ranked++
			if g.Label != "Один лицензионный комплект" || len(g.Rows) != 2 || g.LowestPrice == nil || *g.LowestPrice != 6739 || g.Rows[0].Details.Seller != "softmap" || g.Rows[1].Details.Seller != "softline" || g.Rows[1].Value != 6739.20 {
				t.Fatal("incorrect one-pack comparison", g)
			}
		} else if len(g.Rows) != 1 || g.Rows[0].Details.Seller != "softmagazin" || g.LowestPrice != nil {
			t.Fatal("unknown price basis must remain separate", g)
		}
		for _, r := range g.Rows {
			if r.ProductKey != checkSKU {
				t.Fatal("nonexact match")
			}
			t.Logf("%s %s %.2f %s compared=%v", r.Details.Seller, r.ProductKey, r.Value, r.Details.PriceBasis, g.Comparable)
			count++
		}
	}
	if count != 3 || ranked != 1 {
		t.Fatalf("%d rows and %d ranked groups", count, ranked)
	}
	// An isolated synthetic event verifies the same database path used by the
	// Changes tab, without attributing a made-up price to a real SKU.
	one := int64(1)
	row := catalog.Offer{Source: "softmap", SKU: "QA-SQL-CHANGE", Name: "Synthetic SQL history fixture", URL: "https://softmap.ru/qa/synthetic/", Currency: "RUB", PriceBasis: "displayed_total", Quantity: &one, CapturedAt: "2025-01-01T10:00:00Z", PriceMinor: 12000}
	next := row
	next.CapturedAt = "2025-01-03T12:00:00Z"
	next.PriceMinor = 9900
	if err := s.storeOffers(ctx, []catalog.Offer{row, next}); err != nil {
		t.Fatal(err)
	}
	hist, err := s.Changes(ctx, ChangesInput{ProductKey: row.SKU, From: "2025-01-03", To: "2025-01-03"})
	if err != nil || len(hist.Observations) != 1 || hist.Observations[0].Value != -21 {
		t.Fatal(hist, err)
	}
	t.Log("Synthetic comparison across UTC period boundary: 120.00 to 99.00 RUB")
}
