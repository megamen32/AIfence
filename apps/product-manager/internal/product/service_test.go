package product

import (
	"testing"

	"agent/internal/decision"
	"agent/internal/evidence"
)

func TestBrowserEvidenceLevel(t *testing.T) {
	h := decision.Hypothesis{ProductKey: "example/product"}
	if err := checkEvidenceLevel(h, nil); err != nil {
		t.Fatalf("legacy record: %v", err)
	}

	h.EvidenceLevel = "path_verified"
	h.BrowserEvidenceIDs = []string{"browser-test"}
	row := evidence.Row{ID: "browser-test", Source: "browser", Kind: "path_check", Product: h.ProductKey, Status: "observed", Value: "metrika_add_not_observed"}
	if err := checkEvidenceLevel(h, []evidence.Row{row}); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*evidence.Row){
		"wrong source":  func(r *evidence.Row) { r.Source = "metrika" },
		"wrong kind":    func(r *evidence.Row) { r.Kind = "metric" },
		"wrong product": func(r *evidence.Row) { r.Product = "example/other" },
		"not observed":  func(r *evidence.Row) { r.Status = "estimated" },
		"no verdict":    func(r *evidence.Row) { r.Value = "inconclusive" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := row
			change(&invalid)
			if err := checkEvidenceLevel(h, []evidence.Row{invalid}); err == nil {
				t.Fatal("unverified path accepted")
			}
		})
	}
	if err := checkEvidenceLevel(h, nil); err == nil {
		t.Fatal("unknown observation ID accepted")
	}
}

func TestSignalNotificationKey(t *testing.T) {
	h := decision.Hypothesis{ID: "first", ProductKey: "test/product", SignalIDs: []string{"a", "b"}}
	rows := []evidence.Row{{ID: "b", Status: "observed"}}
	key := notificationKey(h, rows)
	h.ID = "second"
	h.SignalIDs = []string{"b", "a", "b"}
	if key == "" || key != notificationKey(h, rows) {
		t.Fatal("same signal did not retain the delivery key")
	}
	rows[0].Ref.SourceID = "synthetic/test"
	if notificationKey(h, rows) != "" {
		t.Fatal("synthetic signal was eligible for notification")
	}
	rows[0].Ref.SourceID = "aifence-watchdog"
	rows[0].Details = `{"origin":"synthetic_fixture"}`
	if notificationKey(h, rows) != "" {
		t.Fatal("synthetic watchdog signal was eligible for notification")
	}
}
