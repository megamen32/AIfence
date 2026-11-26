package product

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
)

// The import itself is durable before follow-up. Replaying the immutable snapshot retries any unfinished evaluation.
type ImportReceipt struct {
	evidence.Summary
	Followup      []FollowupResult `json:"followup,omitempty"`
	FollowupError string           `json:"followup_error,omitempty"`
}
type FollowupResult struct {
	HypothesisID string `json:"hypothesis_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
}
type followupRow struct {
	evidence.Row
	snapshot string
	meta     struct {
		measurementMeta
		ReleaseID string `json:"release_id"`
	}
}

func (r followupRow) isRelease() bool {
	return r.Source == "release" && r.Kind == "release" && r.Status == "observed" && r.Period.From == r.Period.To && r.meta.HypothesisID != ""
}
func (r followupRow) isOutcome() bool {
	return (r.Source == "sales" || r.Source == "metrika") && r.Kind == "metric" && r.Status == "observed" && r.meta.Kind == "product_outcome" && r.meta.Quality == "comparable" && r.meta.Complete
}
func observationCutoff(raw string) (string, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.DateOnly, raw)
	}
	if err != nil {
		return "", false
	}
	return t.Format(time.DateOnly), true
}

func observedBy(row evidence.Row, documentCutoff string) bool {
	if row.BatchStatus != "ok" && row.BatchStatus != "partial" {
		return false
	}
	cutoff, ok := observationCutoff(row.BatchAsOf)
	return ok && row.Period.To <= cutoff && row.Period.To <= documentCutoff
}

func (s Service) followupRows(ctx context.Context, snapshot string) ([]followupRow, error) {
	var out []followupRow
	for offset := 0; ; offset += 100 {
		page, err := s.Evidence.Read(ctx, evidence.Query{SnapshotID: snapshot, Limit: 100, Offset: offset})
		if err != nil {
			return nil, err
		}
		cutoff, ok := observationCutoff(page.AsOf)
		if !ok {
			return nil, errors.New("для проверки релиза нужны датированные наблюдения")
		}
		for _, r := range page.Rows {
			if !observedBy(r, cutoff) {
				continue
			}
			x := followupRow{Row: r, snapshot: snapshot}
			if json.Unmarshal([]byte(r.Details), &x.meta) != nil {
				continue
			}
			if x.isRelease() || x.isOutcome() {
				out = append(out, x)
			}
		}
		if offset+len(page.Rows) >= page.Total {
			break
		}
	}
	return out, nil
}
func (s Service) Import(ctx context.Context, in evidence.ImportInput) (ImportReceipt, error) {
	summary, err := s.Evidence.Import(ctx, in)
	if err != nil {
		return ImportReceipt{}, err
	}
	result := ImportReceipt{Summary: summary}
	fresh, err := s.followupRows(ctx, summary.SnapshotID)
	if err != nil {
		result.FollowupError = err.Error()
		return result, nil
	}
	var ids, products []string
	for _, r := range fresh {
		if id := r.meta.HypothesisID; id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
		if r.isOutcome() && !slices.Contains(products, r.Product) {
			products = append(products, r.Product)
		}
	}
	if len(ids)+len(products) == 0 {
		return result, nil
	}
	records, err := s.Decisions.PendingMeasurements(ctx, summary.SnapshotID, ids, products)
	if err != nil {
		result.FollowupError = "Не удалось подобрать карточки для оценки. Повторите импорт снимка."
		return result, nil
	}
	cache := map[string][]followupRow{summary.SnapshotID: fresh}
	for _, rec := range records {
		item, err := s.reconcile(ctx, rec, summary.SnapshotID, cache)
		if err != nil {
			item = FollowupResult{HypothesisID: rec.Hypothesis.ID, Status: "retry", Reason: err.Error()}
		}
		if item.Status != "" {
			result.Followup = append(result.Followup, item)
		}
	}
	return result, nil
}
func (s Service) reconcile(ctx context.Context, rec decision.Record, incoming string, cache map[string][]followupRow) (FollowupResult, error) {
	id := rec.Hypothesis.ID
	var rows []followupRow
	seen := map[string]bool{}
	load := func(key string) error {
		if key == "" || seen[key] {
			return nil
		}
		group, ok := cache[key]
		if !ok {
			if err := s.samePolicy(ctx, key, incoming); err != nil {
				return err
			}
			var err error
			group, err = s.followupRows(ctx, key)
			if err != nil {
				return err
			}
			cache[key] = group
		}
		seen[key] = true
		rows = append(rows, group...)
		return nil
	}
	if err := load(rec.EvidenceSnapshotID); err != nil {
		return FollowupResult{}, err
	}
	releasedOn := ""
	if a := rec.Assessment; a != nil {
		releasedOn = a.ReleasedOn
		if prior, _, ok := strings.Cut(a.ReleaseEvidenceRef, "/"); ok {
			if err := load(prior); err != nil {
				return FollowupResult{}, err
			}
		}
	}
	var incomingRelease bool
	for _, row := range cache[incoming] {
		if row.isRelease() && row.Product == rec.Hypothesis.ProductKey && row.meta.HypothesisID == id {
			incomingRelease = true
			if releasedOn == "" {
				releasedOn = row.Period.From
			}
		}
	}
	if incomingRelease {
		if err := load(incoming); err != nil {
			return FollowupResult{}, err
		}
	}
	var sources []string
	for _, row := range rows {
		if (row.Product == rec.Hypothesis.ProductKey || row.Product == rec.Hypothesis.SuccessCriterion.ControlProductKey) && row.Ref.SourceID != "" && !slices.Contains(sources, row.Ref.SourceID) {
			sources = append(sources, row.Ref.SourceID)
		}
	}
	var afterFrom, afterTo string
	if t, err := time.Parse(time.DateOnly, releasedOn); err == nil {
		from, to := decision.MeasurementWindow(t, rec.Hypothesis.SuccessCriterion.ObservationDays)
		afterFrom, afterTo = from.Format(time.DateOnly), to.Format(time.DateOnly)
	}
	related, err := s.Decisions.LinkedMeasurementSnapshots(ctx, rec.Hypothesis, incoming, afterFrom, afterTo, sources)
	if err != nil {
		return FollowupResult{}, err
	}
	for _, key := range append(related, incoming) {
		if err := load(key); err != nil {
			return FollowupResult{}, err
		}
	}
	plan, why := planFollowup(rec, incoming, rows, time.Now().UTC())
	if plan == nil {
		if why != "" {
			return FollowupResult{HypothesisID: id, Status: "awaiting_measurement", Reason: why}, nil
		}
		return FollowupResult{}, nil
	}
	assessment, err := s.Decisions.Evaluate(ctx, *plan)
	if err != nil {
		// A concurrent import may already have committed the terminal, locked decision.
		current, readErr := s.Get(ctx, decision.GetInput{HypothesisID: id})
		if readErr == nil && current.Assessment != nil && (current.Assessment.Status == "supported" || current.Assessment.Status == "rejected") {
			return FollowupResult{HypothesisID: id, Status: current.Assessment.Status}, nil
		}
		return FollowupResult{}, err
	}
	if why == "" {
		why = assessment.Reason
	}
	return FollowupResult{HypothesisID: id, Status: assessment.Status, Reason: why}, nil
}

type releaseBinding struct{ id, date, ref string }

func planFollowup(r decision.Record, incoming string, rows []followupRow, now time.Time) (*decision.EvaluateInput, string) {
	h := r.Hypothesis
	c := h.SuccessCriterion
	if r.Task == nil || h.Status == "supported" || h.Status == "rejected" {
		return nil, ""
	}
	var release releaseBinding
	if a := r.Assessment; a != nil && a.ReleaseID != "" {
		release = releaseBinding{a.ReleaseID, a.ReleasedOn, a.ReleaseEvidenceRef}
	}
	var relevant bool
	for _, row := range rows {
		if row.isRelease() && row.Product == h.ProductKey && row.meta.HypothesisID == h.ID {
			if row.Period.From <= c.BaselineTo || row.Period.From > now.Format(time.DateOnly) {
				continue
			}
			if row.snapshot == incoming {
				relevant = true
			}
			if release.id == "" {
				release = releaseBinding{row.ID, row.Period.From, row.snapshot + "/" + row.ID}
			} else if release.id != row.ID || release.date != row.Period.From {
				return nil, "Для карточки обнаружены разные корректирующие релизы."
			}
		}
		if row.snapshot == incoming && row.isOutcome() && (row.Product == h.ProductKey || row.Product == c.ControlProductKey) && row.Metric == c.MetricKey && (row.meta.HypothesisID == "" || row.meta.HypothesisID == h.ID) {
			relevant = true
		}
	}
	if !relevant || release.id == "" {
		return nil, ""
	}
	released, err := time.Parse(time.DateOnly, release.date)
	if err != nil {
		return nil, "Некорректная дата релиза."
	}
	input := decision.EvaluateInput{HypothesisID: h.ID, ReleaseID: release.id, ReleasedOn: release.date, ReleaseEvidenceRef: release.ref}
	pending := func(reason string) (*decision.EvaluateInput, string) {
		if r.Assessment == nil || r.Assessment.ReleaseID == "" {
			return &input, reason
		}
		return nil, reason
	}
	from, to := decision.MeasurementWindow(released, c.ObservationDays)
	if !to.Before(now.Truncate(24 * time.Hour)) {
		return pending("Окно измерения еще не завершено.")
	}
	before := evidence.Period{From: c.BaselineFrom, To: c.BaselineTo}
	after := evidence.Period{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}
	// Link unlabelled rows only when their source already occurs in the pinned original or release snapshot.
	sources := map[string]bool{}
	for _, row := range rows {
		if row.snapshot == r.EvidenceSnapshotID || strings.HasPrefix(release.ref, row.snapshot+"/") {
			if row.Product == h.ProductKey || row.Product == c.ControlProductKey {
				sources[row.Ref.SourceID] = true
			}
		}
	}
	lookup := func(product string, period evidence.Period) (*decision.PeriodValue, bool) {
		var chosen *decision.PeriodValue
		var ref evidence.SourceRef
		for _, row := range rows {
			if !row.isOutcome() || row.Product != product || row.Metric != c.MetricKey || row.Period != period || row.Unit == "" {
				continue
			}
			if row.meta.HypothesisID != "" && row.meta.HypothesisID != h.ID || row.meta.ReleaseID != "" && row.meta.ReleaseID != release.id {
				continue
			}
			if row.meta.HypothesisID == "" && !sources[row.Ref.SourceID] {
				continue
			}
			if row.snapshot == r.EvidenceSnapshotID && period == after {
				continue
			}
			v, err := strconv.ParseFloat(row.Value, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if chosen != nil {
				if ref.SourceID != row.Ref.SourceID || ref.Locator != row.Ref.Locator || *chosen.Value != v || chosen.Unit != row.Unit || chosen.Segment != row.Segment {
					return nil, false
				}
				continue
			}
			chosen = &decision.PeriodValue{ProductKey: product, MetricKey: c.MetricKey, Unit: row.Unit, Segment: row.Segment, From: period.From, To: period.To, Value: &v, Complete: true, SourceRef: row.snapshot + "/" + row.ID}
			ref = row.Ref
		}
		return chosen, true
	}
	b, ub := lookup(h.ProductKey, before)
	n, un := lookup(h.ProductKey, after)
	var cb, cn *decision.PeriodValue
	var uniqueCB, uniqueCN = true, true
	if c.ControlProductKey != "" {
		cb, uniqueCB = lookup(c.ControlProductKey, before)
		cn, uniqueCN = lookup(c.ControlProductKey, after)
	}
	if !ub || !un || !uniqueCB || !uniqueCN {
		return pending("В одном окне несколько разных наблюдений. Требуется сверка источников.")
	}
	if b == nil || n == nil || c.ControlProductKey != "" && (cb == nil || cn == nil) {
		return pending("Ожидаются полные сопоставимые исходные и последующие замеры.")
	}
	input.Measurement = &decision.Measurement{Quality: "comparable", Kind: "product_outcome", Baseline: *b, Current: *n, ControlBaseline: cb, ControlCurrent: cn}
	return &input, ""
}
