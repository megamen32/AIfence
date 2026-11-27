package handlers

import (
	"encoding/json"
	"slices"
	"strings"

	"agent/internal/evidence"
)

type CauseMetric struct {
	Segment   string   `json:"segment"`
	MetricKey string   `json:"metric_key"`
	Before    *float64 `json:"before_mean"`
	After     *float64 `json:"after_mean"`
	ChangePct *float64 `json:"change_pct"`
	ZScore    *float64 `json:"z_score,omitempty"`
}

type CauseSourceRow struct {
	ID        string             `json:"id"`
	Segment   string             `json:"segment"`
	Period    evidence.Period    `json:"period"`
	Value     *float64           `json:"value"`
	SourceRef evidence.SourceRef `json:"source_ref"`
}

type CauseMeasureWindow struct {
	Period     evidence.Period  `json:"period"`
	WeekCount  *int             `json:"week_count"`
	Total      *float64         `json:"total"`
	WeeklyMean *float64         `json:"weekly_mean"`
	Rows       []CauseSourceRow `json:"rows"`
}

type CauseMeasurementProvenance struct {
	MetricKey string             `json:"metric_key"`
	Segment   string             `json:"segment"`
	Unit      string             `json:"unit"`
	Baseline  CauseMeasureWindow `json:"baseline"`
	Current   CauseMeasureWindow `json:"current"`
}

type CauseRateWindow struct {
	Period    evidence.Period `json:"period"`
	Checkouts *float64        `json:"checkouts"`
	Visits    *float64        `json:"visits"`
	RatePct   *float64        `json:"rate_pct"`
}

type CauseCheckoutPerVisit struct {
	Unit              string          `json:"unit"`
	Baseline          CauseRateWindow `json:"baseline"`
	Current           CauseRateWindow `json:"current"`
	RelativeChangePct *float64        `json:"relative_change_pct"`
}

type CauseMigrationWeek struct {
	ProductKey string           `json:"product_key"`
	Period     evidence.Period  `json:"period"`
	Total      *float64         `json:"total"`
	Rows       []CauseSourceRow `json:"rows"`
}

type CauseMigrationOnset struct {
	MetricKey       string             `json:"metric_key"`
	Unit            string             `json:"unit"`
	OldKeyLastWeek  CauseMigrationWeek `json:"old_key_last_week"`
	NewKeyFirstWeek CauseMigrationWeek `json:"new_key_first_week"`
}

type CauseRelease struct {
	ID         string             `json:"id"`
	Kind       string             `json:"kind"`
	ProductKey string             `json:"product_key"`
	MetricKey  string             `json:"metric_key"`
	Value      json.RawMessage    `json:"value"`
	Unit       string             `json:"unit"`
	Period     evidence.Period    `json:"period"`
	SourceRef  evidence.SourceRef `json:"source_ref"`
	Status     string             `json:"status"`
	Details    json.RawMessage    `json:"details,omitempty"`
}

type CauseReading struct {
	ID                    string                       `json:"id"`
	CauseType             string                       `json:"cause_type"`
	Confirmation          string                       `json:"confirmation"`
	Status                string                       `json:"status"`
	Family                string                       `json:"family"`
	Product               string                       `json:"product"`
	Segment               string                       `json:"segment"`
	Period                evidence.Period              `json:"period"`
	Reason                string                       `json:"reason"`
	CauseEvidence         []CauseMetric                `json:"cause_evidence"`
	MeasurementProvenance []CauseMeasurementProvenance `json:"measurement_provenance,omitempty"`
	CheckoutPerVisit      *CauseCheckoutPerVisit       `json:"checkout_per_visit,omitempty"`
	MigrationOnset        *CauseMigrationOnset         `json:"migration_onset,omitempty"`
	SourceRef             evidence.SourceRef           `json:"source_ref"`
	SourceRefs            []evidence.SourceRef         `json:"source_refs"`
	NativeSourceRefs      []evidence.SourceRef         `json:"native_source_refs,omitempty"`
	CauseRelease          *CauseRelease                `json:"cause_release"`
	WatchdogContext       json.RawMessage              `json:"watchdog_context,omitempty"`
	Origin                string                       `json:"origin,omitempty"`
}

func causeReadings(rows []monitorObservation) []CauseReading {
	var actual, synthetic []CauseReading
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Source != "metrika" || row.Kind != "metric" || row.ID == "" || seen[row.ID] {
			continue
		}
		var details struct {
			Kind                  string                       `json:"kind"`
			Confirmation          string                       `json:"confirmation"`
			CauseType             string                       `json:"cause_type"`
			CauseEvidence         []CauseMetric                `json:"cause_evidence"`
			MeasurementProvenance []CauseMeasurementProvenance `json:"measurement_provenance"`
			CheckoutPerVisit      *CauseCheckoutPerVisit       `json:"checkout_per_visit"`
			MigrationOnset        *CauseMigrationOnset         `json:"migration_onset"`
			CauseRelease          *CauseRelease                `json:"cause_release"`
			SourceRefs            []evidence.SourceRef         `json:"source_refs"`
			NativeSourceRefs      []evidence.SourceRef         `json:"native_source_refs"`
			Reason                string                       `json:"reason"`
			WatchdogContext       json.RawMessage              `json:"watchdog_context"`
			Origin                string                       `json:"origin"`
			Synthetic             bool                         `json:"synthetic"`
		}
		if json.Unmarshal([]byte(row.Details), &details) != nil || details.Kind != "cause_hint" ||
			details.CauseType == "" || row.Metric != details.CauseType {
			continue
		}
		seen[row.ID] = true
		reading := CauseReading{
			ID: row.ID, CauseType: details.CauseType, Confirmation: details.Confirmation, Status: row.Status,
			Family: row.Product, Product: productName(row.Product), Segment: row.Segment, Period: row.Period,
			Reason: details.Reason, CauseEvidence: details.CauseEvidence,
			MeasurementProvenance: details.MeasurementProvenance,
			CheckoutPerVisit:      details.CheckoutPerVisit, MigrationOnset: details.MigrationOnset,
			SourceRef:  row.Ref,
			SourceRefs: details.SourceRefs, NativeSourceRefs: details.NativeSourceRefs, CauseRelease: details.CauseRelease,
			WatchdogContext: details.WatchdogContext, Origin: details.Origin,
		}
		if details.Synthetic || details.Origin == "synthetic_fixture" {
			synthetic = append(synthetic, reading)
		} else {
			actual = append(actual, reading)
		}
	}
	if len(actual) == 0 {
		actual = synthetic
	}
	slices.SortStableFunc(actual, func(a, b CauseReading) int {
		if n := strings.Compare(b.Period.To, a.Period.To); n != 0 {
			return n
		}
		if n := strings.Compare(a.Family, b.Family); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return actual
}
