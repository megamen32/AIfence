package sourcewatch

import (
	"math"
)

// Trends is the observed Google Trends interest for the same phrase and the
// same two completed UTC weeks as the store comparison.
type Trends struct {
	Phrase        string   `json:"phrase"`
	Geo           string   `json:"geo,omitempty"`
	BaselineID    string   `json:"baseline_observation_id"`
	CurrentID     string   `json:"current_observation_id"`
	BaselineIndex int64    `json:"baseline_interest"`
	CurrentIndex  int64    `json:"current_interest"`
	ChangePct     *float64 `json:"relative_change_pct,omitempty"`
}

func extractTrends(b batch, phrase string, baseline, current period) *Trends {
	if b.Status != "ok" || len(b.Observations) != 2 {
		return nil
	}
	rows := map[period]observation{}
	for _, r := range b.Observations {
		if r.ProductKey != phrase || r.MetricKey != "search_interest" || r.SourceRef.SourceID != "google_trends" || r.Status != "observed" || r.Unit != "index" || !bounded(r.ID, 256) || r.Value == nil || *r.Value < 0 || *r.Value > 100 || *r.Value != math.Trunc(*r.Value) {
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
	result := &Trends{Phrase: phrase, BaselineID: a.ID, CurrentID: c.ID, BaselineIndex: int64(*a.Value), CurrentIndex: int64(*c.Value)}
	if result.BaselineIndex > 0 {
		pct := (float64(result.CurrentIndex) - float64(result.BaselineIndex)) / float64(result.BaselineIndex) * 100
		result.ChangePct = &pct
	}
	return result
}
