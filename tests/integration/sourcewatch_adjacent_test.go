package sourcewatch_test

import (
	"agent/internal/sourcewatch"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

type replayManifest struct {
	Inputs        []struct{ Path, SHA256, AsOf string } `json:"inputs"`
	NeutralSHA256 string                                `json:"neutral_sha256"`
}
type replayRow struct {
	Product string                    `json:"product_key"`
	Metric  string                    `json:"metric_key"`
	Segment string                    `json:"segment"`
	Status  string                    `json:"status"`
	Value   *float64                  `json:"value"`
	Unit    string                    `json:"unit"`
	Period  struct{ From, To string } `json:"period"`
	Ref     struct {
		SourceID string `json:"source_id"`
		Locator  string `json:"locator"`
	} `json:"source_ref"`
}
type replayDoc struct {
	AsOf   string            `json:"as_of"`
	Schema string            `json:"schema_version"`
	Source string            `json:"source"`
	Status string            `json:"status"`
	Rows   []json.RawMessage `json:"observations"`
}
type replayKey struct{ product, segment, metric string }

// Runs the actual Check on every eligible pair without reading expected answers.
func TestSourcewatchAllAdjacentWeeks(t *testing.T) {
	root := os.Getenv("BLIND_ADJACENT_ROOT")
	out := os.Getenv("BLIND_REPLAY_OUTPUT")
	if root == "" || out == "" {
		t.Fatal("private input and output paths required")
	}
	read := func(p string) []byte {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	hash := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	var mf replayManifest
	if e := json.Unmarshal(read(filepath.Join(root, "inputs.json")), &mf); e != nil {
		t.Fatal(e)
	}
	neutral := read(filepath.Join(root, "neutral-trends.json"))
	if hash(neutral) != mf.NeutralSHA256 {
		t.Fatal("neutral control changed")
	}
	var ctrl replayDoc
	if e := json.Unmarshal(neutral, &ctrl); e != nil {
		t.Fatal(e)
	}
	trendRows := map[string]json.RawMessage{}
	for _, raw := range ctrl.Rows {
		var r replayRow
		if e := json.Unmarshal(raw, &r); e != nil {
			t.Fatal(e)
		}
		trendRows[r.Product+"\x00"+r.Period.From] = raw
	}
	f, e := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	writer := bufio.NewWriter(f)
	defer writer.Flush()
	enc := json.NewEncoder(writer)
	const met = "62057b9c-7d10-4439-9985-3888e7b259b3"
	const trends = "00000000-0000-4000-8000-000000000003"
	windows := 0
	for _, entry := range mf.Inputs {
		raw := read(entry.Path)
		if hash(raw) != entry.SHA256 {
			t.Fatal("input changed")
		}
		var doc replayDoc
		if e := json.Unmarshal(raw, &doc); e != nil {
			t.Fatal(e)
		}
		if doc.Schema != "aifence.v1" || doc.Source != "metrika" {
			t.Fatal("unexpected source")
		}
		groups := map[replayKey]map[string][]json.RawMessage{}
		for _, rr := range doc.Rows {
			var r replayRow
			if e := json.Unmarshal(rr, &r); e != nil {
				t.Fatal(e)
			}
			if r.Metric != "visits" && r.Metric != "orders" && r.Metric != "revenue" {
				continue
			}
			if r.Status != "observed" || r.Value == nil || r.Unit == "" || r.Product == "" || r.Ref.SourceID == "" || r.Ref.Locator == "" {
				continue
			}
			start, e := time.Parse(time.DateOnly, r.Period.From)
			if e != nil || start.Weekday() != time.Monday {
				continue
			}
			end, e := time.Parse(time.DateOnly, r.Period.To)
			if e != nil || !end.Equal(start.AddDate(0, 0, 6)) || r.Period.To > doc.AsOf[:10] {
				continue
			}
			key := replayKey{r.Product, r.Segment, r.Metric}
			if groups[key] == nil {
				groups[key] = map[string][]json.RawMessage{}
			}
			groups[key][r.Period.From] = append(groups[key][r.Period.From], rr)
		}
		keys := make([]replayKey, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			return a.product+"\x00"+a.segment+"\x00"+a.metric < b.product+"\x00"+b.segment+"\x00"+b.metric
		})
		for _, key := range keys {
			weeks := make([]string, 0, len(groups[key]))
			for week := range groups[key] {
				weeks = append(weeks, week)
			}
			sort.Strings(weeks)
			for i := 1; i < len(weeks); i++ {
				before, _ := time.Parse(time.DateOnly, weeks[i-1])
				current, _ := time.Parse(time.DateOnly, weeks[i])
				if !current.Equal(before.AddDate(0, 0, 7)) {
					continue
				}
				a, b := trendRows[key.product+"\x00"+weeks[i-1]], trendRows[key.product+"\x00"+weeks[i]]
				if a == nil || b == nil {
					t.Fatal("neutral source missing")
				}
				selected := append(append([]json.RawMessage{}, groups[key][weeks[i-1]]...), groups[key][weeks[i]]...)
				metData, _ := json.Marshal(map[string]any{"schema_version": "aifence.v1", "source": "metrika", "status": "ok", "as_of": doc.AsOf, "observations": selected, "warnings": []string{}})
				trendData, _ := json.Marshal(map[string]any{"schema_version": "aifence.v1", "source": "research", "status": "ok", "as_of": doc.AsOf, "observations": []json.RawMessage{a, b}, "warnings": []string{"synthetic neutral control"}})
				w := sourcewatch.Window{MetrikaBinding: met, TrendsBinding: trends, Phrase: key.product, TrendsGeo: "RU", ProductKey: key.product, Segment: key.segment, Metric: key.metric, DropPct: 20, Monday: current.AddDate(0, 0, 7).Format(time.DateOnly)}
				var signal *sourcewatch.Signal
				result, err := sourcewatch.Check(context.Background(), w, true, func(_ context.Context, id string, _ any, dst any) error {
					if id == met {
						return json.Unmarshal(metData, dst)
					}
					if id == trends {
						return json.Unmarshal(trendData, dst)
					}
					return fmt.Errorf("unknown binding")
				}, func(_ context.Context, s sourcewatch.Signal) (string, error) { signal = &s; return "offline-stub", nil })
				row := map[string]any{"input": entry.Path, "input_sha256": entry.SHA256, "product": key.product, "segment": key.segment, "metric": key.metric, "baseline_from": weeks[i-1], "current_from": weeks[i], "monday": w.Monday, "status": result.Status, "signal": signal}
				if err != nil {
					row["error"] = err.Error()
				}
				if e := enc.Encode(row); e != nil {
					t.Fatal(e)
				}
				windows++
			}
		}
	}
	if e := writer.Flush(); e != nil {
		t.Fatal(e)
	}
	if e := f.Sync(); e != nil {
		t.Fatal(e)
	}
	fmt.Printf("Frozen input windows: %d from %d datasets\n", windows, len(mf.Inputs))
}
