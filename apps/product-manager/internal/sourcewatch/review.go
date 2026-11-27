package sourcewatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ReviewInput is what the review task receives: one pinned signal and the
// closed list of evidence IDs it may cite.
type ReviewInput struct {
	Signal      Signal   `json:"signal"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// ReviewOutput is the typed answer. Every cited ID must come from
// ReviewInput.EvidenceIDs; anything else is rejected.
type ReviewOutput struct {
	Hypothesis  string     `json:"hypothesis"`
	EvidenceIDs []string   `json:"evidence_ids"`
	Missing     []string   `json:"missing"`
	Test        ReviewTest `json:"test"`
}

// ReviewTest is the one cheap check the review proposes next.
type ReviewTest struct {
	Metric       string  `json:"metric"`
	ThresholdPct float64 `json:"threshold_pct"`
	Days         int     `json:"days"`
}

// ReviewSignalIDs lists every observation ID the pinned signal carries, in a
// stable order, so the model and the validator agree on the allowed set.
func ReviewSignalIDs(s Signal) []string {
	ids := []string{s.ObservationID}
	if s.Demand != nil {
		ids = append(ids, s.Demand.BaselineID, s.Demand.CurrentID)
	}
	if s.Trends != nil {
		ids = append(ids, s.Trends.BaselineID, s.Trends.CurrentID)
	}
	if s.FamilyChange != nil {
		ids = append(ids, s.FamilyChange.BaselineID, s.FamilyChange.CurrentID)
	}
	for _, f := range s.PriceFamilies {
		ids = append(ids, f.BaselineID, f.CurrentID)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ValidateReview rejects any evidence ID the pinned signal does not carry.
func ValidateReview(out ReviewOutput, allowed []string) (ReviewOutput, error) {
	out.Hypothesis = strings.TrimSpace(out.Hypothesis)
	if out.Hypothesis == "" || len(out.Hypothesis) > 600 {
		return ReviewOutput{}, errors.New("hypothesis must contain 1 to 600 characters")
	}
	known := map[string]bool{}
	for _, id := range allowed {
		known[id] = true
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(out.EvidenceIDs))
	for _, id := range out.EvidenceIDs {
		if !known[id] {
			return ReviewOutput{}, fmt.Errorf("unknown evidence ID %q", id)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ReviewOutput{}, errors.New("cite at least one evidence ID from the signal")
	}
	missing := make([]string, 0, len(out.Missing))
	for _, m := range out.Missing {
		if m = strings.TrimSpace(m); m != "" && len(m) <= 300 {
			missing = append(missing, m)
		}
	}
	out.EvidenceIDs, out.Missing = ids, missing
	if strings.TrimSpace(out.Test.Metric) == "" || len(out.Test.Metric) > 120 {
		return ReviewOutput{}, errors.New("test.metric must contain 1 to 120 characters")
	}
	if out.Test.ThresholdPct <= 0 || out.Test.ThresholdPct > 100 || math.IsNaN(out.Test.ThresholdPct) || math.IsInf(out.Test.ThresholdPct, 0) {
		return ReviewOutput{}, errors.New("test.threshold_pct must be greater than zero and at most 100")
	}
	if out.Test.Days < 1 || out.Test.Days > 90 {
		return ReviewOutput{}, errors.New("test.days must be between 1 and 90")
	}
	return out, nil
}

// MarshalReview persists the exact typed input alongside the pinned signal.
func MarshalReview(in ReviewInput) ([]byte, error) { return json.Marshal(in) }
