package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

type ContextBatch = EvidenceBatch[string]
type ContextFact = EvidenceObservation[string]

type WatchContext struct {
	SchemaVersion string                  `json:"schema_version"`
	Synthetic     *bool                   `json:"synthetic"`
	Batches       map[string]ContextBatch `json:"batches"`
}

type ContextWindow struct {
	From       string `json:"from"`
	To         string `json:"to"`
	ProductKey string `json:"product_key,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type MetricWindow struct {
	ContextWindow
	MetricKeys []string `json:"metric_keys,omitempty"`
}

type BacklogFilter struct {
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	ProductKey string   `json:"product_key,omitempty"`
	SourcePath string   `json:"source_path,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	MetricKeys []string `json:"metric_keys"`
}

type InterviewFilter struct {
	Keywords   []string `json:"keywords"`
	ProductKey string   `json:"product_key,omitempty"`
	SourcePath string   `json:"source_path,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type Watch struct {
	series      Service
	contextPath func(context.Context) string
}

func registerWatch(a *agentsdk.Agent, w Watch) {
	a.RegisterTool(tool.Typed[ContextWindow, ContextBatch]("releases_in_window").
		Description("Read release records overlapping an inclusive window, with original source locators.").
		Execute(w.Releases).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[BacklogFilter, ContextBatch]("backlog_for_metrics").
		Description("Read backlog items linked to exact metric keys; optionally filter by active dates.").
		Execute(w.Backlog).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[InterviewFilter, ContextBatch]("interview_search").
		Description("Find short attributed excerpts using literal keywords in the authorized interview index.").
		Execute(w.Interviews).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[MetricWindow, ContextBatch]("data_breaks_in_window").
		Description("Read known collection breaks, missing instrumentation and incomplete windows.").
		Execute(w.Breaks).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[SignalInput, SignalBatch]("detect_signal").
		Description("Evaluate the week containing to against previous weeks and a control. Null is inconclusive.").
		Execute(w.Detect).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[SignalInput, SignalBatch]("replay_signals").
		Description("Replay weekly watchdog decisions in date order without using future observations.").
		Execute(w.Replay).Build(), agentsdk.AccessUser)
}

func (w Watch) loadContext(ctx context.Context, path string) (WatchContext, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".json") {
		return WatchContext{}, fmt.Errorf("select an authorized watchdog context JSON file")
	}
	raw, err := w.series.read(ctx, path)
	if err != nil || len(raw) > 8<<20 {
		return WatchContext{}, fmt.Errorf("watchdog context unavailable or too large")
	}
	var out WatchContext
	if json.Unmarshal(raw, &out) != nil || out.SchemaVersion != "aifence.watch-context.v1" || out.Synthetic == nil || out.Batches == nil {
		return WatchContext{}, fmt.Errorf("invalid watchdog context")
	}
	kinds := map[string]string{"release": "release", "backlog": "task", "research": "research", "metrika": "metric"}
	ids := map[string]bool{}
	for source, batch := range out.Batches {
		_, dateErr := time.Parse(time.RFC3339, batch.AsOf)
		if kinds[source] == "" || batch.Source != source || batch.SchemaVersion != "aifence.v1" ||
			batch.Observations == nil || dateErr != nil || !validBatchStatus(batch.Status) {
			return WatchContext{}, fmt.Errorf("invalid watchdog context batch")
		}
		for _, fact := range batch.Observations {
			if ids[fact.ID] || fact.ID == "" || fact.Kind != kinds[source] || fact.ProductKey == "" ||
				fact.MetricKey == "" || fact.Unit == "" || fact.SourceRef.SourceID == "" || fact.SourceRef.Locator == "" {
				return WatchContext{}, fmt.Errorf("invalid context observation or provenance")
			}
			ids[fact.ID] = true
			if _, err := days(fact.Period); err != nil {
				return WatchContext{}, err
			}
			switch fact.Status {
			case "observed", "incomparable", "estimated":
			case "missing":
				if fact.Value != nil {
					return WatchContext{}, fmt.Errorf("missing context observation must have a null value")
				}
			default:
				return WatchContext{}, fmt.Errorf("invalid context observation status")
			}
			if source == "metrika" && fact.MetricKey == "data_break" {
				switch detailString(fact.Details, "effect") {
				case "exclude", "boundary", "missing":
				default:
					return WatchContext{}, fmt.Errorf("unknown data break effect")
				}
			}
		}
	}
	return out, nil
}

func validBatchStatus(s string) bool { return s == "ok" || s == "partial" || s == "unavailable" }

func detailString(details map[string]any, name string) string {
	v, _ := details[name].(string)
	return v
}

func matchesProduct(fact, requested string) bool {
	return requested == "" || fact == "all" || fact == requested
}

func overlaps(a, b Period) bool {
	return (b.To == "" || a.From <= b.To) && (b.From == "" || a.To >= b.From)
}

func terms(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 12 {
		return nil, fmt.Errorf("specify 1–12 search terms")
	}
	out := make([]string, 0, len(values))
	for _, val := range values {
		s := strings.ToLower(strings.TrimSpace(val))
		if s == "" || len(s) > 200 {
			return nil, fmt.Errorf("invalid search term")
		}
		out = append(out, s)
	}
	return out, nil
}

func metricMatch(fact ContextFact, keys []string) bool {
	if len(keys) == 0 {
		return true
	}
	if fact.MetricKey == "data_break" && len(metricLinks(fact)) == 0 {
		return true
	}
	for _, metric := range metricLinks(fact) {
		for _, key := range keys {
			if metric == key || metric == "family_orders_idx" && key == "orders_index" ||
				metric == "orders_index" && key == "family_orders_idx" {
				return true
			}
		}
	}
	return false
}

func metricLinks(fact ContextFact) []string {
	var links []string
	items, _ := fact.Details["metric_keys"].([]any)
	for _, item := range items {
		if key, ok := item.(string); ok {
			links = append(links, key)
		}
	}
	return links
}

func (w Watch) Releases(ctx context.Context, in ContextWindow) (ContextBatch, error) {
	window := Period{in.From, in.To}
	if _, err := days(window); err != nil {
		return ContextBatch{}, err
	}
	return w.find(ctx, in.SourcePath, "release", in.Limit, func(f ContextFact) bool {
		return matchesProduct(f.ProductKey, in.ProductKey) && overlaps(f.Period, window)
	})
}

func (w Watch) Backlog(ctx context.Context, in BacklogFilter) (ContextBatch, error) {
	keys, err := terms(in.MetricKeys)
	if err != nil {
		return ContextBatch{}, err
	}
	window := Period{in.From, in.To}
	if in.From != "" || in.To != "" {
		if _, err := days(window); err != nil {
			return ContextBatch{}, err
		}
	}
	return w.find(ctx, in.SourcePath, "backlog", in.Limit, func(f ContextFact) bool {
		return matchesProduct(f.ProductKey, in.ProductKey) && overlaps(f.Period, window) && metricMatch(f, keys)
	})
}

func (w Watch) Breaks(ctx context.Context, in MetricWindow) (ContextBatch, error) {
	window := Period{in.From, in.To}
	if _, err := days(window); err != nil {
		return ContextBatch{}, err
	}
	keys := in.MetricKeys
	if len(keys) > 0 {
		var err error
		keys, err = terms(keys)
		if err != nil {
			return ContextBatch{}, err
		}
	}
	return w.find(ctx, in.SourcePath, "metrika", in.Limit, func(f ContextFact) bool {
		return f.MetricKey == "data_break" && matchesProduct(f.ProductKey, in.ProductKey) &&
			overlaps(f.Period, window) && metricMatch(f, keys)
	})
}

func (w Watch) Interviews(ctx context.Context, in InterviewFilter) (ContextBatch, error) {
	keys, err := terms(in.Keywords)
	if err != nil {
		return ContextBatch{}, err
	}
	out, err := w.find(ctx, in.SourcePath, "research", in.Limit, func(f ContextFact) bool {
		if f.Value == nil || !matchesProduct(f.ProductKey, in.ProductKey) {
			return false
		}
		for _, key := range keys {
			if strings.Contains(strings.ToLower(*f.Value), key) {
				return true
			}
		}
		return false
	})
	for i := range out.Observations {
		f := &out.Observations[i]
		excerpt := matchedExcerpt(*f.Value, keys)
		f.Value = &excerpt
	}
	return out, err
}

func matchedExcerpt(text string, keys []string) string {
	lower := strings.ToLower(text)
	at := len(lower)
	for _, key := range keys {
		if idx := strings.Index(lower, key); idx >= 0 {
			at = min(at, idx)
		}
	}
	runes := []rune(text)
	pos := utf8.RuneCountInString(lower[:at])
	start := max(0, pos-85)
	end := min(len(runes), start+300)
	excerpt := string(runes[start:end])
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt
}

func (w Watch) find(ctx context.Context, path, source string, limit int, match func(ContextFact) bool) (ContextBatch, error) {
	out := ContextBatch{"aifence.v1", source, stamp(), "unavailable", []ContextFact{}, []string{}}
	if limit < 0 || limit > 50 {
		return out, fmt.Errorf("limit must be between 1 and 50")
	}
	if limit == 0 {
		limit = 15
	}
	path = chosen(path, w.contextPath(ctx))
	if path == "" {
		out.Warnings = append(out.Warnings, "Configure watch_context_path or select an authorized context file.")
		return out, nil
	}
	catalog, err := w.loadContext(ctx, path)
	if err != nil {
		return out, err
	}
	src, ok := catalog.Batches[source]
	if !ok {
		out.Warnings = append(out.Warnings, "This source was not supplied in the selected context file.")
		return out, nil
	}
	out.AsOf, out.Status = src.AsOf, src.Status
	out.Warnings = append(out.Warnings, src.Warnings...)
	for _, f := range src.Observations {
		if match(f) {
			out.Observations = append(out.Observations, f)
		}
	}
	sort.SliceStable(out.Observations, func(i, j int) bool {
		return out.Observations[i].Period.From < out.Observations[j].Period.From
	})
	if len(out.Observations) > limit {
		out.Observations = out.Observations[:limit]
		out.Status = "partial"
		out.Warnings = append(out.Warnings, "Results truncated by limit.")
	}
	return out, nil
}
