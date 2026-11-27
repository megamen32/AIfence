package competitor

import (
	"context"
	"testing"
)

func TestNormalizePublicSiteRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://example.com",
		"https://127.0.0.1/",
		"https://localhost/",
		"https://user:pass@example.com/",
	} {
		if _, _, err := normalizePublicSite(context.Background(), raw); err == nil {
			t.Fatalf("normalizePublicSite(%q) accepted unsafe URL", raw)
		}
	}
}

func TestUnsafeHostLiteralRejectsPrivateTargets(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "10.0.0.4", "service.internal", "foo.local"} {
		if !unsafeHostLiteral(host) {
			t.Fatalf("host %q should be rejected", host)
		}
	}
	if unsafeHostLiteral("example.com") {
		t.Fatal("public hostname should be allowed")
	}
}

func TestSameSiteHostAllowsWWWAndSubdomain(t *testing.T) {
	if !sameSiteHost("example.com", "www.example.com") {
		t.Fatal("www redirect should stay on candidate site")
	}
	if !sameSiteHost("shop.example.com", "example.com") {
		t.Fatal("subdomain redirect should stay on candidate site")
	}
	if sameSiteHost("example.com", "example.org") {
		t.Fatal("cross-site redirect should be rejected")
	}
}

func TestRowsFromSnapshotProducesNonPriceEvidence(t *testing.T) {
	rows := rowsFromSnapshot("example.com", "https://example.com/", "software", 200, siteSnapshot{
		Title:        "Example",
		Description:  "Software catalog",
		H1:           []string{"Products"},
		Links:        []siteLink{{Text: "Buy now", Href: "https://example.com/buy"}, {Text: "Login", Href: "https://example.com/login"}, {Text: "Support", Href: "https://example.com/support"}},
		NavLinks:     4,
		SearchInputs: 1,
		Forms:        2,
		Body:         "Products and support",
	})
	if len(rows) != 6 {
		t.Fatalf("got %d rows, want 6", len(rows))
	}
	seen := map[string]competitorObservation{}
	for _, row := range rows {
		seen[row.Block] = row
	}
	for _, block := range []string{"search", "purchase", "account", "service", "ux"} {
		row, ok := seen[block]
		if !ok || row.Status != "observed" || row.Score == nil {
			t.Fatalf("block %q lacks observed scored row: %#v", block, row)
		}
	}
	if row := seen["price"]; row.Status != "not_connected" || row.Score != nil {
		t.Fatalf("price row must remain external: %#v", row)
	}
}

func TestUnknownSiteRowsKeepPriceExternal(t *testing.T) {
	rows := unknownSiteRows("https://example.com/", "software", "timeout")
	if len(rows) != 6 {
		t.Fatalf("got %d rows, want 6", len(rows))
	}
	for _, row := range rows {
		if row.Block == "price" {
			if row.Status != "not_connected" || row.Score != nil {
				t.Fatalf("price row must remain not_connected: %#v", row)
			}
			continue
		}
		if row.Status != "unknown" || row.Score != nil {
			t.Fatalf("unknown row was scored: %#v", row)
		}
	}
}
