package sourcewatch

import (
	"math"
	"strings"
)

// FamilyChange is a two-week change computed from an explicitly labelled
// product family snapshot. It is used only when Metrika has not already
// published the change for this exact product, segment and window.
type FamilyChange struct {
	ProductKey  string   `json:"product_key"`
	Segment     string   `json:"segment,omitempty"`
	Metric      string   `json:"metric"`
	Unit        string   `json:"unit"`
	BaselineID  string   `json:"baseline_observation_id"`
	CurrentID   string   `json:"current_observation_id"`
	BaselineRef string   `json:"baseline_ref"`
	CurrentRef  string   `json:"current_ref"`
	Baseline    float64  `json:"baseline_value"`
	Current     float64  `json:"current_value"`
	ChangePct   *float64 `json:"relative_change_pct"`
	SourceID    string   `json:"source_id"`
	Revision    string   `json:"revision,omitempty"`
	Synthetic   bool     `json:"synthetic"`
}

// productDelta returns a change Metrika already computed for the product.
func productDelta(b batch, product, segment, metric string, current period) *observation {
	var delta *observation
	for i := range b.Observations {
		o := &b.Observations[i]
		if o.ProductKey != product || o.MetricKey != metric+".relative_change_pct" || o.Period != current || !segmentMatches(o.Segment, segment) {
			continue
		}
		if o.Status != "observed" || o.Unit != "percent" || o.Value == nil || !bounded(o.ID, 256) || !bounded(o.SourceRef.SourceID, 128) || delta != nil {
			return nil
		}
		delta = o
	}
	return delta
}

// familyChange computes the two-week change from raw observed weekly rows of
// one product family and one segment. It keeps both source IDs and locators,
// uses the unit as published, and never mixes segments or families.
func familyChange(b batch, product, segment, metric string, before, current period) *FamilyChange {
	var a, c *observation
	for i := range b.Observations {
		o := &b.Observations[i]
		if o.ProductKey != product || o.MetricKey != metric || !segmentMatches(o.Segment, segment) || o.Status != "observed" || o.Value == nil {
			continue
		}
		if !bounded(o.ID, 256) || !bounded(o.SourceRef.SourceID, 128) || !bounded(o.SourceRef.Locator, 512) {
			return nil
		}
		if math.IsNaN(*o.Value) || math.IsInf(*o.Value, 0) {
			return nil
		}
		switch o.Period {
		case before:
			if a != nil {
				return nil
			}
			a = o
		case current:
			if c != nil {
				return nil
			}
			c = o
		}
	}
	if a == nil || c == nil || a.ID == c.ID || a.Unit != c.Unit || a.SourceRef.SourceID != c.SourceRef.SourceID || a.SourceRef.Revision != c.SourceRef.Revision {
		return nil
	}
	out := &FamilyChange{
		ProductKey: product, Segment: segment, Metric: metric, Unit: a.Unit,
		BaselineID: a.ID, CurrentID: c.ID, BaselineRef: a.SourceRef.Locator, CurrentRef: c.SourceRef.Locator,
		Baseline: *a.Value, Current: *c.Value, SourceID: a.SourceRef.SourceID, Revision: a.SourceRef.Revision,
		Synthetic: detailBool(a.Details, "synthetic") || detailBool(c.Details, "synthetic"),
	}
	if out.Baseline > 0 {
		pct := (out.Current - out.Baseline) / out.Baseline * 100
		out.ChangePct = &pct
	}
	return out
}

func segmentMatches(rowSegment, want string) bool {
	return want == "" || strings.EqualFold(rowSegment, want)
}

func detailBool(details map[string]any, key string) bool {
	value, _ := details[key].(bool)
	return value
}
