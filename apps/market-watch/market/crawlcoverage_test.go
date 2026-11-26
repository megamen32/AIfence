package market

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCoverageImportAndMonotonicRead(t *testing.T) {
	s := New(memoryStore{})
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }
	empty, err := s.CrawlCoverage(t.Context(), struct{}{})
	if err != nil || empty.AsOf != "" {
		t.Fatalf("empty: %+v, %v", empty, err)
	}
	when := at.Format(time.RFC3339)
	report := CrawlReport{AsOf: when, Sources: []CrawlSource{
		{Source: "softline", Known: true, Total: 3, Pending: 1, Priced: 2, AsOf: when, InventoryAt: when},
		{Source: "softmagazin"},
		{Source: "softmap", Known: true, Total: 2, NoPublicPrice: 1, Unresolved: 1, AsOf: when, InventoryAt: when},
	}}
	submit := func(r CrawlReport) error {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return s.ImportCoverage(httptest.NewRecorder(), httptest.NewRequest("POST", "/coverage/import", bytes.NewReader(b)))
	}
	if err := submit(report); err != nil {
		t.Fatal(err)
	}
	if err := submit(report); err != nil {
		t.Fatal(err)
	}
	report.AsOf = at.Add(time.Minute).Format(time.RFC3339)
	if err := submit(report); err != nil {
		t.Fatal(err)
	}
	report.AsOf = when
	if err := submit(report); err == nil {
		t.Fatal("accepted older report")
	}
	current, err := s.CrawlCoverage(t.Context(), struct{}{})
	if err != nil || current.AsOf == when || current.Sources[1].Known {
		t.Fatalf("latest: %+v, %v", current, err)
	}
	report.Sources[1].Pending = 1
	if err := report.Validate(at); err == nil {
		t.Fatal("invented unknown denominator")
	}
	report.Sources[1].Pending = 0
	report.Sources[0].Pending = 0
	if err := report.Validate(at); err == nil {
		t.Fatal("nonpartitioning URL counts")
	}
}
