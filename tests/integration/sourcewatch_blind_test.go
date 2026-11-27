package sourcewatch_test

import (
	"agent/internal/sourcewatch"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

type envelope struct {
	AsOf    string            `json:"as_of"`
	Source  string            `json:"source"`
	Batches []json.RawMessage `json:"batches"`
}

type offlineResult struct {
	Input    string `json:"input"`
	SHA256   string `json:"sha256"`
	Monday   string `json:"monday"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Launched bool   `json:"simulated_launch"`
}

// Inputs are loaded as delivered. Missing sources are errors, not fabricated observations.
func TestBlindSourcewatchReplay(t *testing.T) {
	patterns := []string{
		filepath.Join(os.Getenv("BLIND_HISTORICAL"), "run-*", "inputs", "case-*", "metrika.json"),
		filepath.Join(os.Getenv("BLIND_CURRENT"), "inputs", "case-*", "metrika.json"),
		filepath.Join(os.Getenv("BLIND_MULTI"), "inputs", "case-*", "snapshot.json"),
	}
	var paths []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("no blind inputs")
	}
	var results []offlineResult
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc envelope
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		stamp, err := time.Parse(time.RFC3339, doc.AsOf)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		monday := stamp.UTC().Truncate(24 * time.Hour)
		for monday.Weekday() != time.Monday {
			monday = monday.AddDate(0, 0, 1)
		}
		metrika, market := []byte(nil), []byte(nil)
		if doc.Source == "metrika" {
			metrika = raw
		}
		for _, b := range doc.Batches {
			var meta envelope
			if err := json.Unmarshal(b, &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Source == "metrika" {
				metrika = b
			}
			if meta.Source == "market" && market == nil {
				market = b
			}
		}
		const metID = "62057b9c-7d10-4439-9985-3888e7b259b3"
		const marketID = "44e7584f-1caf-4f08-9bd9-b69006cdd0af"
		w := sourcewatch.Window{MetrikaBinding: metID, MarketBinding: marketID, SKU: "KL4141RCEFR", Metric: "visits", DropPct: 20, Monday: monday.Format(time.DateOnly)}
		launched := false
		result, checkErr := sourcewatch.Check(context.Background(), w, true,
			func(_ context.Context, binding string, _ any, dst any) error {
				var payload []byte
				if binding == metID {
					payload = metrika
				} else if binding == marketID {
					payload = market
				}
				if len(payload) == 0 {
					return errors.New("source not present in this input")
				}
				return json.Unmarshal(payload, dst)
			},
			func(_ context.Context, _ sourcewatch.Signal) (string, error) {
				launched = true
				return "offline-stub", nil
			},
		)
		hash := sha256.Sum256(raw)
		row := offlineResult{Input: path, SHA256: hex.EncodeToString(hash[:]), Monday: w.Monday, Status: result.Status, Launched: launched}
		if checkErr != nil {
			row.Error = checkErr.Error()
		}
		results = append(results, row)
	}
	output := os.Getenv("BLIND_REPLAY_OUTPUT")
	if output == "" {
		t.Fatal("BLIND_REPLAY_OUTPUT required")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Frozen offline replay records: %d\n", len(results))
}
