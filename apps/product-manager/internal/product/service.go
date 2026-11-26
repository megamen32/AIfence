package product

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type Service struct {
	Decisions     *decision.Service
	Evidence      *evidence.Service
	Notifications *SignalNotifications
	Mail          *SignalMail
}

func (s Service) Save(ctx context.Context, in decision.SaveInput) (decision.Record, error) {
	if in.ExpectedVersion > 0 {
		old, err := s.Get(ctx, decision.GetInput{HypothesisID: in.Hypothesis.ID})
		if err != nil {
			return decision.Record{}, err
		}
		if err = s.samePolicy(ctx, old.EvidenceSnapshotID, in.EvidenceSnapshotID); err != nil {
			return decision.Record{}, err
		}
		if in.ContextIDs == nil {
			in.ContextIDs = old.ContextIDs
		}
	}
	h := in.Hypothesis
	ids := append([]string{}, h.SignalIDs...)
	ids = append(ids, h.SupportIDs...)
	ids = append(ids, h.CounterIDs...)
	ids = append(ids, h.BrowserEvidenceIDs...)
	ids = append(ids, in.ContextIDs...)
	rows, err := s.Evidence.Verify(ctx, in.EvidenceSnapshotID, ids)
	if err != nil {
		return decision.Record{}, err
	}
	if err := checkEvidenceLevel(h, rows); err != nil {
		return decision.Record{}, err
	}
	record, err := s.Decisions.Save(ctx, in)
	if err == nil && in.ExpectedVersion == 0 {
		s.Notifications.publish(ctx, record, rows)
	}
	return record, err
}

func checkEvidenceLevel(h decision.Hypothesis, rows []evidence.Row) error {
	byID := make(map[string]evidence.Row, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	verified := false
	for _, id := range h.BrowserEvidenceIDs {
		row := byID[id]
		if row.Source != "browser" || row.Kind != "path_check" || row.Product != h.ProductKey {
			return fmt.Errorf("наблюдение %s должно быть браузерной проверкой пути для этого продукта", id)
		}
		verified = verified || verifiedPath(row)
	}
	switch h.EvidenceLevel {
	case "", "signal_only", "correlation":
		return nil
	case "path_verified":
		if verified {
			return nil
		}
		return errors.New("для path_verified нужен наблюдаемый исход проверки пути")
	default:
		return errors.New("неизвестный уровень доказанности")
	}
}

func verifiedPath(row evidence.Row) bool {
	if row.Status != "observed" {
		return false
	}
	switch row.Value {
	case "path_broken", "metrika_add_not_observed", "ok":
		return true
	}
	return false
}

// Chat selects evidence IDs; dates, values and quality come from the imported snapshot.
type EvaluateInput struct {
	HypothesisID         string `json:"hypothesis_id"`
	SnapshotID           string `json:"snapshot_id,omitempty"`
	ReleaseObservationID string `json:"release_observation_id,omitempty"`
	BaselineID           string `json:"baseline_id,omitempty"`
	CurrentID            string `json:"current_id,omitempty"`
	ControlBaselineID    string `json:"control_baseline_id,omitempty"`
	ControlCurrentID     string `json:"control_current_id,omitempty"`
}
type measurementMeta struct {
	Complete     bool   `json:"complete"`
	Quality      string `json:"quality"`
	Kind         string `json:"measurement_kind"`
	HypothesisID string `json:"hypothesis_id"`
}

func (s Service) Evaluate(ctx context.Context, in EvaluateInput) (decision.Assessment, error) {
	record, err := s.Get(ctx, decision.GetInput{HypothesisID: in.HypothesisID})
	if err != nil {
		return decision.Assessment{}, err
	}
	if in.SnapshotID != "" {
		if err = s.samePolicy(ctx, record.EvidenceSnapshotID, in.SnapshotID); err != nil {
			return decision.Assessment{}, err
		}
	}
	input := decision.EvaluateInput{HypothesisID: in.HypothesisID}
	ids := []string{}
	for _, id := range []string{in.ReleaseObservationID, in.BaselineID, in.CurrentID, in.ControlBaselineID, in.ControlCurrentID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return s.Decisions.Evaluate(ctx, input)
	}
	if in.SnapshotID == "" {
		return decision.Assessment{}, errors.New("укажите снимок для замеров и релиза")
	}
	data, err := s.Evidence.Read(ctx, evidence.Query{SnapshotID: in.SnapshotID, IDs: ids, Limit: 10})
	if err != nil {
		return decision.Assessment{}, err
	}
	rows := map[string]evidence.Row{}
	for _, r := range data.Rows {
		rows[r.ID] = r
	}
	for _, id := range ids {
		if _, ok := rows[id]; !ok {
			return decision.Assessment{}, fmt.Errorf("наблюдение отсутствует: %s", id)
		}
	}
	if in.ReleaseObservationID != "" {
		r := rows[in.ReleaseObservationID]
		var meta measurementMeta
		_ = json.Unmarshal([]byte(r.Details), &meta)
		if r.Source != "release" || r.Status != "observed" || r.Period.From != r.Period.To || r.Product != record.Hypothesis.ProductKey || meta.HypothesisID != in.HypothesisID {
			return decision.Assessment{}, errors.New("нужен подтвержденный релиз с привязкой к этой гипотезе")
		}
		input.ReleaseID = r.ID
		input.ReleasedOn = r.Period.From
		input.ReleaseEvidenceRef = in.SnapshotID + "/" + r.ID
	}
	if in.BaselineID != "" || in.CurrentID != "" || in.ControlBaselineID != "" || in.ControlCurrentID != "" {
		if in.CurrentID == "" || in.BaselineID == "" {
			return decision.Assessment{}, errors.New("нужны обе сравниваемые выборки")
		}
		m := decision.Measurement{}
		var kind string
		fill := func(id string) (*decision.PeriodValue, error) {
			if id == "" {
				return nil, nil
			}
			r := rows[id]
			var meta measurementMeta
			_ = json.Unmarshal([]byte(r.Details), &meta)
			if (r.Source != "sales" && r.Source != "metrika") || r.Status != "observed" || meta.Quality != "comparable" || (meta.Kind != "product_outcome" && meta.Kind != "measurement_repair") || (kind != "" && meta.Kind != kind) {
				return nil, errors.New("нужны сопоставимые наблюдения одной метрики с проверенным качеством")
			}
			kind = meta.Kind
			v, err := strconv.ParseFloat(r.Value, 64)
			if err != nil {
				return nil, errors.New("измерение должно быть числом")
			}
			return &decision.PeriodValue{ProductKey: r.Product, MetricKey: r.Metric, Unit: r.Unit, Segment: r.Segment, From: r.Period.From, To: r.Period.To, SourceRef: in.SnapshotID + "/" + r.ID, Value: &v, Complete: meta.Complete}, nil
		}
		b, err := fill(in.BaselineID)
		if err != nil {
			return decision.Assessment{}, err
		}
		c, err := fill(in.CurrentID)
		if err != nil {
			return decision.Assessment{}, err
		}
		m.Baseline = *b
		m.Current = *c
		m.ControlBaseline, err = fill(in.ControlBaselineID)
		if err != nil {
			return decision.Assessment{}, err
		}
		m.ControlCurrent, err = fill(in.ControlCurrentID)
		if err != nil {
			return decision.Assessment{}, err
		}
		m.Kind = kind
		m.Quality = "comparable"
		input.Measurement = &m
	}
	return s.Decisions.Evaluate(ctx, input)
}

func (s Service) Get(ctx context.Context, in decision.GetInput) (decision.Record, error) {
	r, err := s.Decisions.Get(ctx, in)
	if err != nil {
		return decision.Record{}, err
	}
	if err := s.Evidence.Access(ctx, r.EvidenceSnapshotID); err != nil {
		return decision.Record{}, err
	}
	return r, nil
}
func (s Service) List(ctx context.Context, _ evidence.Empty) (decision.ListResult, error) {
	r, err := s.Decisions.List(ctx, struct{}{})
	if err != nil {
		return decision.ListResult{}, err
	}
	result := decision.ListResult{Records: []decision.Record{}}
	for _, rec := range r.Records {
		err := s.Evidence.Access(ctx, rec.EvidenceSnapshotID)
		if errors.Is(err, evidence.ErrLocalOnly) {
			continue
		}
		if err != nil {
			return decision.ListResult{}, err
		}
		result.Records = append(result.Records, rec)
	}
	return result, nil
}
func (s Service) Send(ctx context.Context, in decision.SendInput) (decision.Task, error) {
	if _, err := s.Get(ctx, decision.GetInput{HypothesisID: in.HypothesisID}); err != nil {
		return decision.Task{}, err
	}
	return s.Decisions.Send(ctx, in)
}

func (s Service) samePolicy(ctx context.Context, first, second string) error {
	if first == second {
		return nil
	}
	a, e := s.Evidence.Policy(ctx, first)
	if e != nil {
		return e
	}
	b, e := s.Evidence.Policy(ctx, second)
	if e != nil {
		return e
	}
	if a != b {
		return errors.New("для карточки и связанного измерения требуется одинаковый режим доступа")
	}
	return nil
}
