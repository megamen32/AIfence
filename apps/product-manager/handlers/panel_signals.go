package handlers

import (
	"agent/internal/decision"
	"agent/views"
	"math"
	"slices"
	"strings"
	"time"
)

// A source slice belongs to its own viewer mode; unlabeled context can follow
// an unambiguous snapshot, and a mixed snapshot requires row-level provenance.
func separateMonitorRows(rows []monitorObservation, demo bool) []monitorObservation {
	modes := map[string]uint8{}
	for _, row := range rows {
		if educationalObservation(row.Row, row.details) {
			modes[row.snapshot] |= 2
		} else if actualObservation(row) {
			modes[row.snapshot] |= 1
		}
	}
	var out []monitorObservation
	for _, row := range rows {
		synthetic := educationalObservation(row.Row, row.details)
		actual := !synthetic && (actualObservation(row))
		if (demo && (synthetic || !actual && row.Metric != watchdogMetric && modes[row.snapshot] == 2)) ||
			(!demo && !synthetic && (actual || row.Metric != watchdogMetric && modes[row.snapshot] == 1)) {
			out = append(out, row)
		} else if !demo && !synthetic && row.Source == "market" && row.Metric == "displayed_price" {
			out = append(out, row)
		}
	}
	return out
}

func actualObservation(row monitorObservation) bool {
	return row.details.Origin == "imported_historical" || row.details.Origin == "live_api"
}

func signalPlanning(records []decision.Record, row monitorObservation) (string, string, *float64) {
	for _, rec := range records {
		p := rec.Planning
		if rec.EvidenceSnapshotID != row.snapshot || rec.Hypothesis.ProductKey != row.Product ||
			!slices.Contains(rec.Hypothesis.SignalIDs, row.ID) || p == nil || p.WeeklyPotentialRUB == nil || p.Basis == "" {
			continue
		}
		n := *p.WeeklyPotentialRUB
		if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			continue
		}
		return rub(n) + "/нед", p.Basis, p.WeeklyPotentialRUB
	}
	return "", "", nil
}

func firstSignalLabel(signal monitorObservation, rows []monitorObservation) string {
	if t, err := time.Parse(time.RFC3339, signal.details.FirstDetected); err == nil {
		return "Впервые обнаружено " + t.UTC().Format("02.01.2006, 15:04 UTC")
	}
	// Earlier timestamps only belong to this open episode, until the last
	// observed reset. ImportedAt is deliberately excluded from detection.
	var first time.Time
	key := seriesOf(signal)
	lastReset := ""
	for _, row := range rows {
		if row.Metric == watchdogMetric && seriesOf(row) == key && row.Status == "observed" &&
			row.Value == "false" && row.Period.To < signal.Period.To && row.Period.To > lastReset {
			lastReset = row.Period.To
		}
	}
	for _, row := range rows {
		if row.Metric != watchdogMetric || seriesOf(row) != key || row.Status != "observed" ||
			row.Period.To > signal.Period.To || row.Period.To <= lastReset || row.Value != "true" {
			continue
		}
		value := row.details.FirstDetected
		if value == "" {
			value = row.details.Detected
		}
		if t, err := time.Parse(time.RFC3339, value); err == nil && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	if first.IsZero() {
		return ""
	}
	return "Впервые обнаружено " + first.UTC().Format("02.01.2006, 15:04 UTC")
}

func linkedRecord(record decision.Record, rows []monitorObservation, signal *monitorObservation) bool {
	if signal != nil {
		return record.EvidenceSnapshotID == signal.snapshot && slices.Contains(record.Hypothesis.SignalIDs, signal.ID)
	}
	for _, row := range rows {
		if row.snapshot == record.EvidenceSnapshotID && row.Product == record.Hypothesis.ProductKey &&
			slices.Contains(record.Hypothesis.SignalIDs, row.ID) {
			return true
		}
	}
	return false
}

func signalCoverage(rows []monitorObservation, key, snapshot string) []views.CoverageItem {
	covered := map[string]bool{}
	for _, row := range rows {
		if row.Product == key && row.snapshot == snapshot && row.Status == "observed" && row.Value != "null" {
			source := row.Source
			if source == "metrika" && (strings.HasPrefix(row.Metric, "cfg_") || strings.HasPrefix(row.Metric, "configurator_")) {
				source = "configurator"
			}
			covered[source] = true
		}
	}
	out := []views.CoverageItem{}
	for _, source := range []string{"sales", "configurator", "metrika", "research", "market", "browser"} {
		name := label(sourceTitles, source)
		if source == "configurator" {
			name = "Конфигуратор"
		}
		out = append(out, views.CoverageItem{Name: name, Available: covered[source]})
	}
	return out
}
