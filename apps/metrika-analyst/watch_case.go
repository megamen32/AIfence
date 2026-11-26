package main

import (
	"context"
	"encoding/json"
	"fmt"
)

type WatchCaseInput struct {
	Series         SignalInput `json:"series"`
	ContextMetrics []string    `json:"context_metrics,omitempty"`
	Keywords       []string    `json:"keywords,omitempty"`
}

type WatchCaseResult struct {
	Status         string             `json:"status"`
	FirstAlertWeek *Period            `json:"first_alert_week"`
	Document       *WatchCaseDocument `json:"document"`
	Timeline       []WatchCaseWeek    `json:"timeline"`
	Warnings       []string           `json:"warnings"`
}

type WatchCaseWeek struct {
	Week   string `json:"week"`
	Alert  *bool  `json:"alert"`
	Reason string `json:"reason"`
}

type WatchCaseDocument struct {
	SchemaVersion string      `json:"schema_version"`
	SourcePolicy  string      `json:"source_policy,omitempty"`
	AsOf          string      `json:"as_of"`
	Batches       []CaseBatch `json:"batches"`
	Warnings      []string    `json:"warnings"`
}

// The case envelope carries both boolean watchdog decisions and textual source facts.
type CaseBatch struct {
	SchemaVersion string   `json:"schema_version"`
	Source        string   `json:"source"`
	AsOf          string   `json:"as_of"`
	Status        string   `json:"status"`
	Observations  []any    `json:"observations"`
	Warnings      []string `json:"warnings"`
}

func caseBatch[V any](batch EvidenceBatch[V]) CaseBatch {
	out := CaseBatch{
		SchemaVersion: batch.SchemaVersion, Source: batch.Source, AsOf: batch.AsOf, Status: batch.Status,
		Observations: make([]any, 0, len(batch.Observations)), Warnings: append([]string{}, batch.Warnings...),
	}
	for _, row := range batch.Observations {
		out.Observations = append(out.Observations, row)
	}
	return out
}

func (w Watch) CollectCase(ctx context.Context, in WatchCaseInput) (WatchCaseResult, error) {
	result := WatchCaseResult{Status: "incomplete", Timeline: []WatchCaseWeek{}, Warnings: []string{}}
	var err error
	if len(in.ContextMetrics) > 0 {
		in.ContextMetrics, err = terms(in.ContextMetrics)
		if err != nil {
			return result, err
		}
	}
	if len(in.Keywords) > 0 {
		in.Keywords, err = terms(in.Keywords)
		if err != nil {
			return result, err
		}
	}
	replay, err := w.Replay(ctx, in.Series)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, replay.Warnings...)
	var alarm *Signal
	quiet := len(replay.Observations) > 0 && replay.Status == "ok"
	for i := range replay.Observations {
		week := &replay.Observations[i]
		entry := WatchCaseWeek{Week: week.Period.From, Reason: detailString(week.Details, "reason")}
		if week.Value == nil || week.Status != "observed" {
			quiet = false
		} else {
			entry.Alert = week.Value
			if *week.Value && alarm == nil {
				alarm = week
			}
		}
		result.Timeline = append(result.Timeline, entry)
	}
	if alarm == nil {
		if quiet {
			result.Status = "quiet"
		}
		return result, nil
	}
	baseline, ok := alarm.Details["baseline_period"].(Period)
	if !ok {
		return result, fmt.Errorf("watchdog alert has no baseline window")
	}
	query := in.Series
	window := Period{baseline.From, alarm.Period.To}
	contextWindow := ContextWindow{
		From: window.From, To: window.To, ProductKey: query.ProductKey,
		SourcePath: query.ContextPath, Limit: 12,
	}
	keys := in.ContextMetrics
	if len(keys) == 0 {
		keys = []string{query.MetricKey}
	}

	collector := []struct {
		source string
		read   func() (ContextBatch, error)
	}{
		{"release", func() (ContextBatch, error) { return w.Releases(ctx, contextWindow) }},
		{"backlog", func() (ContextBatch, error) {
			return w.Backlog(ctx, BacklogFilter{From: window.From, To: window.To, ProductKey: query.ProductKey,
				SourcePath: query.ContextPath, Limit: contextWindow.Limit, MetricKeys: keys})
		}},
		{"metrika", func() (ContextBatch, error) { return w.Breaks(ctx, MetricWindow{ContextWindow: contextWindow}) }},
		{"research", func() (ContextBatch, error) {
			if len(in.Keywords) == 0 {
				return ContextBatch{}, fmt.Errorf("no interview keywords supplied")
			}
			return w.Interviews(ctx, InterviewFilter{Keywords: in.Keywords, ProductKey: query.ProductKey,
				SourcePath: query.ContextPath, Limit: 8})
		}},
	}
	doc := WatchCaseDocument{
		SchemaVersion: "aifence.case.v1", AsOf: replay.AsOf, Batches: []CaseBatch{caseBatch(replay)},
	}
	if detailString(alarm.Details, "origin") != "synthetic_fixture" {
		doc.SourcePolicy = "local_project_only"
	}
	for _, source := range collector {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		batch, err := source.read()
		if err != nil {
			reason := "Evidence source unavailable."
			if source.source == "research" && len(in.Keywords) == 0 {
				reason = "No interview keywords supplied."
			}
			batch = ContextBatch{"aifence.v1", source.source, replay.AsOf, "unavailable", []ContextFact{}, []string{reason}}
		}
		facts := batch.Observations[:0]
		for _, fact := range batch.Observations {
			// Later notes and undated interviews recorded outside the window do not belong in this case.
			if !overlaps(fact.Period, window) || detailString(fact.Details, "known_from") > window.To {
				continue
			}
			facts = append(facts, fact)
			if synthetic, _ := fact.Details["synthetic"].(bool); !synthetic {
				doc.SourcePolicy = "local_project_only"
			}
		}
		batch.Observations = facts
		if len(facts) == 0 && batch.Status == "ok" {
			batch.Status = "partial"
			batch.Warnings = append(batch.Warnings, "No matching observations in the alert window.")
		}
		if batch.Status != "ok" {
			result.Warnings = append(result.Warnings, source.source+": "+batch.Status)
		}
		doc.Batches = append(doc.Batches, caseBatch(batch))
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	doc.Warnings = result.Warnings
	encoded, err := json.Marshal(doc)
	if err != nil {
		return result, err
	}
	if len(encoded) > 768<<10 {
		return result, fmt.Errorf("watchdog case exceeds 768 KiB; shorten the replay range")
	}
	result.Status, result.FirstAlertWeek, result.Document = "alert", &alarm.Period, &doc
	return result, nil
}
