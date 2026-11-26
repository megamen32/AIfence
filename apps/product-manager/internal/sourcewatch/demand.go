package sourcewatch

import (
	"bytes"
	"encoding/json"
	"math"
	"time"
)

type Demand struct {
	Phrase        string   `json:"phrase"`
	BaselineID    string   `json:"baseline_observation_id"`
	CurrentID     string   `json:"current_observation_id"`
	BaselineCount int64    `json:"baseline_queries"`
	CurrentCount  int64    `json:"current_queries"`
	ChangePct     *float64 `json:"relative_change_pct,omitempty"`
}

func extractDemand(b batch, phrase string, baseline, current period) *Demand {
	if b.Status != "ok" || len(b.Observations) != 2 {
		return nil
	}
	rows := map[period]observation{}
	for _, r := range b.Observations {
		if r.ProductKey != phrase || r.MetricKey != "search_queries" || r.SourceRef.SourceID != "yandex-wordstat:search-api" || r.Status != "observed" || r.Unit != "count" || !bounded(r.ID, 256) || r.Value == nil || *r.Value < 0 || *r.Value > 9007199254740991 || *r.Value != math.Trunc(*r.Value) {
			return nil
		}
		rows[r.Period] = r
	}
	a, ok := rows[baseline]
	if !ok {
		return nil
	}
	c, ok := rows[current]
	if !ok || a.ID == c.ID {
		return nil
	}
	result := &Demand{Phrase: phrase, BaselineID: a.ID, CurrentID: c.ID, BaselineCount: int64(*a.Value), CurrentCount: int64(*c.Value)}
	if result.BaselineCount > 0 {
		pct := (*c.Value - *a.Value) / *a.Value * 100
		result.ChangePct = &pct
	}
	return result
}

// unavailableFor distinguishes the producer's explicit, current empty response
// from absent fields, stale receipts and contradictory records.
func (b batch) unavailableFor(monday time.Time) bool {
	if b.Status != "unavailable" || b.Observations == nil || len(b.Observations) != 0 || b.Warnings == nil {
		return false
	}
	stamp, err := time.Parse(time.RFC3339, b.AsOf)
	return err == nil && !stamp.Before(monday)
}

// Preserve extensions on ordinary source reads. A Wordstat unavailable envelope
// can release a store-only signal, so check that exact evidence-v1 envelope.
func (b *batch) UnmarshalJSON(raw []byte) error {
	type fields batch
	if err := json.Unmarshal(raw, (*fields)(b)); err != nil {
		return err
	}
	if b.Source != "wordstat" || b.Status != "unavailable" || len(b.Observations) > 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*fields)(b))
}
