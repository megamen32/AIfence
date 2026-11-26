//go:build airlockintegration

package market

import (
	catalog "agent/collector"
	"agent/internal/db"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMatrixAndHistorySQL(t *testing.T) {
	dsn := os.Getenv("MARKET_QA_PG")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
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
	if _, err = tx.ExecContext(ctx, "SET LOCAL search_path TO pg_temp"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("MARKET_QA_FILES")
	for _, filename := range []string{"00001_market.sql", "00003_market_explorer.sql"} {
		data, err := os.ReadFile(dir + "/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(data), "-- +goose Down")
		if _, err = tx.ExecContext(ctx, strings.ReplaceAll(up, "CREATE TABLE", "CREATE TEMP TABLE")); err != nil {
			t.Fatal(err)
		}
	}
	s := New(memoryStore{})
	s.queries = db.New(tx)
	one, ten := int64(1), int64(10)
	a := catalog.Offer{Source: "softline", SKU: "S", PriceBasis: "per_unit", Quantity: &one, MaxQuantity: &ten, Name: "New License 5 seats 1 year", PriceMinor: 10000, Currency: "RUB", URL: "https://store.softline.ru/s", CapturedAt: "2026-11-25T03:00:00Z"}
	b := a
	b.PriceMinor = 9000
	b.CapturedAt = "2026-11-26T03:00:00Z"
	flat := a
	flat.CapturedAt = "2026-11-25T06:00:00Z"
	otherTier := a
	otherTier.MaxQuantity = &one
	unknown := a
	unknown.SKU = ""
	unknown.URL = "https://store.softline.ru/no-sku"
	if err := s.storeOffers(ctx, []catalog.Offer{b, a, flat, otherTier, unknown}); err != nil {
		t.Fatal(err)
	}
	h, err := s.PriceHistory(ctx, ChangesInput{"s", "2026-11-25", "2026-11-26"})
	if err != nil || h.Truncated || len(h.Series) != 2 {
		t.Fatal(h, err)
	}
	n := 0
	for _, series := range h.Series {
		n += len(series.Points)
		if len(series.Points) == 3 && (series.Points[0].Value != 100 || series.Points[1].Value != 100 || series.Points[2].Value != 90) {
			t.Fatal(series)
		}
	}
	if n != 4 {
		t.Fatal("wrong observation count", n)
	}
	start, _ := time.Parse("2006-01-02", "2026-11-20")
	page, last, err := s.matrixPage(ctx, "", 1, start, "http://example.localhost")
	if err != nil || last != "sku:S" || len(page) != 2 {
		t.Fatal(page, last, err)
	}
	found := false
	for _, l := range page {
		if l[10] == "-10.00" && l[17] == "10.00" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing measured change", page)
	}
	page, last, err = s.matrixPage(ctx, last, 1, start, "http://example.localhost")
	if err != nil || len(page) != 1 || page[0][2] != "" || !strings.Contains(page[0][15], "tab=catalog") {
		t.Fatal(page, last, err)
	}
	page, _, err = s.matrixPage(ctx, last, 1, start, "http://example.localhost")
	if err != nil || len(page) != 0 {
		t.Fatal("end-of-page failure", page, err)
	}
	// A local release check can use saved public observations as an optional fixture.
	if _, err := os.Stat(dir + "/softline.jsonl"); err == nil {
		var actual []catalog.Offer
		for _, source := range matrixSources {
			visitExport(t, dir+"/"+source+".jsonl", func(o catalog.Offer) {
				if o.SKU == checkSKU {
					actual = append(actual, o)
				}
			})
		}
		if err = s.storeOffers(ctx, actual); err != nil {
			t.Fatal(err)
		}
		h, err = s.PriceHistory(ctx, ChangesInput{checkSKU, "2026-11-26", "2026-11-26"})
		if err != nil || len(h.Series) != 3 {
			t.Fatal("saved sample history", h, err)
		}
		page, _, err = s.matrixPage(ctx, "", 100, start, "")
		if err != nil {
			t.Fatal(err)
		}
		exact := false
		for _, l := range page {
			if l[2] == checkSKU && l[9] == "0.20" && l[6] == "6739.20" && l[7] == "6739.00" {
				exact = true
			}
			if l[2] == checkSKU {
				t.Log(l[2], l[6:10], "comparable", l[16])
			}
		}
		if !exact {
			t.Fatal("known actual sample not ranked correctly", page)
		}
	}
}
