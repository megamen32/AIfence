//go:build airlockintegration

package market

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "agent/collector"
	"agent/internal/db"
	_ "github.com/lib/pq"
)

const checkSKU = "KL4141RCEFS"

func pageFixture(dir, u string) string {
	return filepath.Join(dir, fmt.Sprintf("%x.html", sha256.Sum256([]byte(u))))
}

func visitExport(t *testing.T, path string, fn func(catalog.Offer)) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 65536), 4<<20)
	for scan.Scan() {
		var o catalog.Offer
		if err := json.Unmarshal(scan.Bytes(), &o); err != nil {
			t.Fatal(err)
		}
		fn(o)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
}

// Explicit fixture preparation against public stores using the production HTTP policy.
func TestCaptureMarketPages(t *testing.T) {
	dir := os.Getenv("MARKET_PAGE_CAPTURE")
	if dir == "" {
		t.Skip("explicit live fixture capture")
	}
	srcs, err := catalog.Sources()
	if err != nil {
		t.Fatal(err)
	}
	net := catalog.NewHTTP()
	for _, s := range srcs {
		set := map[string]bool{}
		for _, u := range s.Seeds {
			set[u] = true
		}
		visitExport(t, filepath.Join(dir, s.ID+".jsonl"), func(o catalog.Offer) {
			if o.SKU == checkSKU {
				set[o.URL] = true
			}
		})
		for u := range set {
			b, err := net.Fetch(t.Context(), s, u)
			if err != nil {
				t.Fatal(s.ID, err)
			}
			if err := os.WriteFile(pageFixture(dir, u), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "captured-at"), []byte(time.Now().UTC().Format(time.RFC3339)), 0600); err != nil {
		t.Fatal(err)
	}
}

// The transaction always rolls back. Only temporary tables may be created.
// Files must be copies of all three published JSONL exports and public page fixtures.
func TestWholeImportAndPriceRefresh(t *testing.T) {
	dsn, dir := os.Getenv("MARKET_QA_PG"), os.Getenv("MARKET_QA_FILES")
	if dsn == "" || dir == "" {
		t.Skip("requires an explicitly selected isolated PostgreSQL transaction and public catalog exports")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, err := os.ReadFile(filepath.Join(dir, "migration.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err := tx.ExecContext(t.Context(), "SET LOCAL search_path TO pg_temp;"+strings.ReplaceAll(up, "CREATE TABLE", "CREATE TEMP TABLE")); err != nil {
		t.Fatal(err)
	}
	s := New(memoryStore{})
	s.queries = db.New(tx)
	captured, err := os.ReadFile(filepath.Join(dir, "captured-at"))
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(string(captured)))
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return stamp }
	s.fetch = func(_ context.Context, _ catalog.Source, u string) ([]byte, error) {
		return os.ReadFile(pageFixture(dir, u))
	}
	sources, err := catalog.Sources()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]catalog.Offer{}
	importAll := func() {
		for _, src := range sources {
			var rows []catalog.Offer
			count := 0
			flush := func() {
				if len(rows) == 0 {
					return
				}
				if err := checkBatch(rows, sources); err != nil {
					t.Fatal(src.ID, err)
				}
				if err := s.storeOffers(t.Context(), rows); err != nil {
					t.Fatal(src.ID, err)
				}
				rows = rows[:0]
			}
			visitExport(t, filepath.Join(dir, src.ID+".jsonl"), func(o catalog.Offer) {
				if o.SKU == checkSKU {
					expected[src.ID] = o
				}
				rows = append(rows, o)
				count++
				if len(rows) == 200 {
					flush()
				}
			})
			flush()
			t.Logf("%s JSONL: %d observations", src.ID, count)
		}
	}
	check := func(label string) {
		ev, err := s.List(t.Context(), ListInput{ProductKey: checkSKU})
		if err != nil || len(ev.Observations) != 3 {
			t.Fatalf("%s: expected exactly 3 indexed comparisons, got %+v (%v)", label, ev, err)
		}
		seen := map[string]bool{}
		for _, o := range ev.Observations {
			want, ok := expected[o.Details.Seller]
			if !ok || seen[o.Details.Seller] || o.ProductKey != checkSKU || o.Value != float64(want.PriceMinor)/100 || o.Details.PriceBasis != want.PriceBasis || o.SourceRef.URL != want.URL {
				t.Fatalf("%s: mismatched row %+v expected %+v", label, o, want)
			}
			if (want.MaxQuantity == nil) != (o.Details.MaxQuantity == nil) || (want.MaxQuantity != nil && *want.MaxQuantity != *o.Details.MaxQuantity) || (want.Quantity == nil) != (o.Details.Quantity == nil) || (want.Quantity != nil && *want.Quantity != *o.Details.Quantity) {
				t.Fatal("quantity tier changed", label, o)
			}
			seen[o.Details.Seller] = true
			t.Logf("%s %s %.2f %s", label, o.Details.Seller, o.Value, o.Details.PriceBasis)
		}
	}
	importAll()
	check("full import")
	importAll()
	check("replay")
	if _, err := s.Refresh(t.Context(), RefreshInput{ProductKey: checkSKU}); err != nil {
		t.Fatal(err)
	}
	check("native refresh")
	importAll()
	check("replay after refresh")
}
