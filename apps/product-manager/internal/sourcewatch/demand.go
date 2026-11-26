package sourcewatch

import "math"

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
