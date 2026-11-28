package evidence

import (
	"encoding/json"
	"testing"
)

func TestRowsKeepIdentityMissingAndFilters(t *testing.T) {
	raw := json.RawMessage(`{"id":"m","metric_key":"orders","product_key":"kaspersky_small_office_security","value":null,"period":{"from":"2025-05-01","to":"2025-05-02"},"source_ref":{"source_id":"fixture","locator":"a2"},"status":"missing","kind":"metric","unit":"index"}`)
	b := Batch{Schema: "aifence.v1", Source: "sales", Status: "partial", AsOf: "2025-05-02T00:00:00Z", Observations: []json.RawMessage{raw}, Warnings: []string{}}
	d := Document{Schema: "aifence.case.v1", AsOf: b.AsOf, Batches: []Batch{b}, SourcePolicy: "local_project_only"}
	blob, _ := json.Marshal(d)
	got, n, err := parse(blob)
	if err != nil || n != 1 || got.SourcePolicy != d.SourcePolicy {
		t.Fatal(n, err)
	}
	found, err := selectRows(d, Query{IDs: []string{"m"}, Limit: 1})
	if err != nil || found.Rows[0].Value != "null" || found.Rows[0].Ref.Locator != "a2" {
		t.Fatal(found, err)
	}
	skip, _ := selectRows(d, Query{Offset: 1})
	if skip.Total != 1 || len(skip.Rows) != 0 {
		t.Fatal(skip)
	}
	d.Batches = append(d.Batches, b)
	blob, _ = json.Marshal(d)
	if _, _, err = parse(blob); err == nil {
		t.Fatal("duplicate observation ID accepted")
	}
}

func TestReadAcrossRejectsBadPaging(t *testing.T) {
	var s Service
	if _, err := s.readAcross(nil, Query{Offset: -1}); err == nil {
		t.Fatal("a negative offset must be rejected before any lookup")
	}
	if _, err := selectRows(Document{}, Query{Limit: 500}); err != nil {
		t.Fatal("a limit above one page is clamped, not an error:", err)
	}
}
