package sourcewatch

// This file runs the real Check against evidence files on disk. Inputs and the
// oracle are kept apart: the oracle names only the expected status, never the
// values, so a wrong calculation cannot be hidden by a matching expectation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func caseDir(root, name string) string { return filepath.Join(root, name) }

type fileCase struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Window Window `json:"window"`
	// MondayOverride replaces the Monday the file's week is pinned to.
	Monday string `json:"monday,omitempty"`
	// Each source is a raw evidence file, decoded by the same Read adapter a
	// host would use. No value is injected by the test.
	Metrika  string `json:"metrika"`
	Market   string `json:"market"`
	Wordstat string `json:"wordstat"`
	Trends   string `json:"trends"`
	Approved bool   `json:"approved"`
	Want     string `json:"want"`
	// WantTrigger and WantPriceFamilies are asserted only when non-empty.
	WantTrigger       string `json:"want_trigger"`
	WantPriceFamilies int    `json:"want_price_families"`
	WantErrorContains string `json:"want_error_contains"`
}

func fileRead(root string) Read {
	return func(_ context.Context, binding string, _, output any) error {
		name := map[string]string{metBinding: "metrika", marketBinding: "market", demandBinding: "wordstat", trendsBinding: "trends"}[binding]
		if name == "" {
			return os.ErrNotExist
		}
		raw, err := os.ReadFile(filepath.Join(root, name+".json"))
		if err != nil {
			return err
		}
		switch out := output.(type) {
		case *batch:
			return json.Unmarshal(raw, out)
		case *weekDelta:
			return json.Unmarshal(raw, out)
		}
		return os.ErrInvalid
	}
}

func TestCheckOnEvidenceFiles(t *testing.T) {
	root := os.Getenv("SOURCEWATCH_CASE_DIR")
	if root == "" {
		t.Skip("SOURCEWATCH_CASE_DIR not set; this is the explicit file-based run")
	}
	// Every *cases.json in the directory is one frozen input set. Each file is
	// read as delivered; the oracle inside names only the expected status.
	manifests, err := filepath.Glob(filepath.Join(root, "*cases.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no case manifests in %s: %v", root, err)
	}
	var cases []fileCase
	for _, manifest := range manifests {
		raw, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var set []fileCase
		if err = json.Unmarshal(raw, &set); err != nil {
			t.Fatalf("%s: %v", manifest, err)
		}
		cases = append(cases, set...)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			starts := 0
			w := tc.Window
			if tc.Monday != "" {
				w.Monday = tc.Monday
			}
			out, err := Check(t.Context(), w, tc.Approved, fileRead(caseDir(root, tc.Dir)), func(_ context.Context, s Signal) (string, error) {
				starts++
				if w.ProductKey != "" && (s.ProductKey != w.ProductKey || s.FamilyChange == nil) {
					t.Errorf("family provenance lost: %+v", s)
				}
				if tc.WantTrigger != "" && s.Trigger != tc.WantTrigger {
					t.Errorf("trigger %q want %q", s.Trigger, tc.WantTrigger)
				}
				if tc.WantPriceFamilies > 0 && len(s.PriceFamilies) != tc.WantPriceFamilies {
					t.Errorf("price families %d want %d", len(s.PriceFamilies), tc.WantPriceFamilies)
				}
				return "review-run", nil
			})
			if tc.WantErrorContains != "" {
				if err == nil || !contains(err.Error(), tc.WantErrorContains) {
					t.Fatalf("err %v want containing %q", err, tc.WantErrorContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Status != tc.Want {
				t.Fatalf("status %q want %q", out.Status, tc.Want)
			}
			if (starts == 1) != (tc.Want == "submitted") {
				t.Fatalf("starts %d for status %q", starts, tc.Want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
