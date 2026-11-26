package decision

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

// A planning scenario is authored separately from measured product outcomes.
type PlanningEstimate struct {
	WeeklyPotentialRUB *float64 `json:"weekly_potential_rub,omitempty"`
	CheckHours         *float64 `json:"check_hours,omitempty"`
	Basis              string   `json:"basis,omitempty"`
}
type EstimateInput struct {
	HypothesisID    string           `json:"hypothesis_id"`
	ExpectedVersion int64            `json:"expected_version"`
	Estimate        PlanningEstimate `json:"estimate"`
}
type PriorityItem struct {
	HypothesisID                string            `json:"hypothesis_id"`
	Title                       string            `json:"title"`
	EvidenceLevel               string            `json:"evidence_level"`
	Version                     int64             `json:"version"`
	Estimate                    *PlanningEstimate `json:"estimate,omitempty"`
	WeeklyPotentialPerCheckHour *float64          `json:"weekly_potential_rub_per_check_hour,omitempty"`
}
type Priorities struct {
	Ranked        []PriorityItem `json:"ranked"`
	NeedsEstimate []PriorityItem `json:"needs_estimate"`
}

func (p PlanningEstimate) normalized() (*PlanningEstimate, error) {
	p.Basis = strings.TrimSpace(p.Basis)
	if p.WeeklyPotentialRUB == nil && p.CheckHours == nil && p.Basis == "" {
		return nil, nil
	}
	if p.WeeklyPotentialRUB == nil && p.CheckHours == nil {
		return nil, errors.New("Введите сумму или трудоёмкость проверки.")
	}
	if p.Basis == "" || utf8.RuneCountInString(p.Basis) > 500 {
		return nil, errors.New("Укажите источник или допущение длиной до 500 символов.")
	}
	if n := p.WeeklyPotentialRUB; n != nil && (!finite(*n) || *n < 0 || *n > 1e15) {
		return nil, errors.New("Потенциал должен быть конечной неотрицательной суммой до 10¹⁵ ₽ в неделю.")
	}
	if n := p.CheckHours; n != nil && (!finite(*n) || *n < 0.01 || *n > 1e7) {
		return nil, errors.New("Трудоёмкость проверки должна быть от 0,01 до 10 миллионов человеко-часов.")
	}
	return &p, nil
}
func evidenceOrder(s string) int {
	switch s {
	case "path_verified":
		return 2
	case "correlation":
		return 1
	default:
		return 0
	}
}
func rankDrafts(records []Record) Priorities {
	out := Priorities{Ranked: []PriorityItem{}, NeedsEstimate: []PriorityItem{}}
	for _, r := range records {
		h := r.Hypothesis
		if r.Task != nil || h.Status != "proposed" {
			continue
		}
		level := h.EvidenceLevel
		if level == "" {
			level = "signal_only"
		}
		i := PriorityItem{HypothesisID: h.ID, Title: h.Title, Version: r.Version, EvidenceLevel: level, Estimate: r.Planning}
		if p := i.Estimate; p != nil && p.CheckHours != nil && p.WeeklyPotentialRUB != nil && *p.CheckHours > 0 {
			score := *p.WeeklyPotentialRUB / *p.CheckHours
			i.WeeklyPotentialPerCheckHour = &score
			out.Ranked = append(out.Ranked, i)
		} else {
			out.NeedsEstimate = append(out.NeedsEstimate, i)
		}
	}
	tie := func(a, b PriorityItem) bool {
		if x, y := evidenceOrder(a.EvidenceLevel), evidenceOrder(b.EvidenceLevel); x != y {
			return x > y
		}
		var ah, bh *float64
		if a.Estimate != nil {
			ah = a.Estimate.CheckHours
		}
		if b.Estimate != nil {
			bh = b.Estimate.CheckHours
		}
		if ah != nil && bh != nil && *ah != *bh {
			return *ah < *bh
		}
		if (ah != nil) != (bh != nil) {
			return ah != nil
		}
		return a.HypothesisID < b.HypothesisID
	}
	sort.Slice(out.Ranked, func(i, j int) bool {
		a, b := out.Ranked[i], out.Ranked[j]
		if x, y := *a.WeeklyPotentialPerCheckHour, *b.WeeklyPotentialPerCheckHour; x != y {
			return x > y
		}
		return tie(a, b)
	})
	sort.Slice(out.NeedsEstimate, func(i, j int) bool { return tie(out.NeedsEstimate[i], out.NeedsEstimate[j]) })
	return out
}
func (s *Service) RankedDrafts(ctx context.Context, allowLocal bool) (Priorities, error) {
	who, err := owner(ctx)
	if err != nil {
		return Priorities{}, err
	}
	rows, err := s.q.PMDraftsForReview(ctx, db.PMDraftsForReviewParams{OwnerID: who, LocalAllowed: allowLocal})
	if err != nil {
		return Priorities{}, err
	}
	records := make([]Record, 0, len(rows))
	for _, raw := range rows {
		r, err := decode(raw, nil)
		if err != nil {
			return Priorities{}, err
		}
		records = append(records, r)
	}
	return rankDrafts(records), nil
}
func (s *Service) SetEstimate(ctx context.Context, in EstimateInput) (Record, error) {
	who, err := owner(ctx)
	if err != nil {
		return Record{}, err
	}
	estimate, err := in.Estimate.normalized()
	if err != nil {
		return Record{}, err
	}
	var out Record
	err = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		r, err := decode(db.New(tx).PMLock(ctx, db.PMLockParams{OwnerID: who, HypothesisID: in.HypothesisID}))
		if err != nil {
			return err
		}
		if r.Version != in.ExpectedVersion || r.Task != nil || r.Hypothesis.Status != "proposed" {
			return ErrVersion
		}
		if !reflect.DeepEqual(r.Planning, estimate) {
			r.Planning = estimate
			r.Version++
			if err := s.persist(ctx, tx, who, r); err != nil {
				return err
			}
		}
		out = r
		return nil
	})
	return out, err
}
