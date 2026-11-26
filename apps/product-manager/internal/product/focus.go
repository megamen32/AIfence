package product

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"agent/internal/decision"
	"agent/internal/evidence"
)

type Focus = decision.Focus

func (s Service) GetFocus(ctx context.Context, _ evidence.Empty) (Focus, error) {
	focus, err := s.Decisions.GetFocus(ctx)
	if err != nil || focus.ProductKey == "" {
		return focus, err
	}
	if focus.SnapshotID == "" {
		if !s.Evidence.CanReadLocal(ctx) {
			return Focus{}, nil
		}
		return focus, nil
	}
	q := evidence.Query{SnapshotID: focus.SnapshotID, ProductKey: focus.ProductKey, Limit: 100}
	if focus.HypothesisID != "" {
		record, err := s.Decisions.Get(ctx, decision.GetInput{HypothesisID: focus.HypothesisID})
		if err != nil {
			return Focus{}, err
		}
		q.IDs = record.Hypothesis.SignalIDs
	}
	data, err := s.Evidence.Read(ctx, q)
	if errors.Is(err, evidence.ErrLocalOnly) {
		return Focus{}, nil
	}
	if err != nil {
		return Focus{}, err
	}
	focus.Synthetic = syntheticFocus(data.Rows)
	return focus, nil
}

// Only server-read source metadata can mark a stored focus as synthetic.
func syntheticFocus(rows []evidence.Row) bool {
	for _, row := range rows {
		var provenance struct {
			Origin    string `json:"origin"`
			Synthetic bool   `json:"synthetic"`
		}
		_ = json.Unmarshal([]byte(row.Details), &provenance)
		if provenance.Synthetic || provenance.Origin == "synthetic_fixture" || strings.HasPrefix(row.Ref.SourceID, "synthetic/") || strings.HasPrefix(row.Ref.SourceID, qaLifecyclePrefix) {
			return true
		}
	}
	return false
}

func (s Service) SetFocus(ctx context.Context, in Focus) (Focus, error) {
	in.ProductKey = strings.TrimSpace(in.ProductKey)
	in.Synthetic = false
	if in.HypothesisID != "" {
		record, err := s.Get(ctx, decision.GetInput{HypothesisID: in.HypothesisID})
		if err != nil {
			return Focus{}, err
		}
		if (in.ProductKey != "" && in.ProductKey != record.Hypothesis.ProductKey) ||
			(in.SnapshotID != "" && in.SnapshotID != record.EvidenceSnapshotID) {
			return Focus{}, errors.New("фокус должен совпадать с данными карточки")
		}
		in.ProductKey, in.SnapshotID = record.Hypothesis.ProductKey, record.EvidenceSnapshotID
	} else if in.SnapshotID != "" {
		rows, err := s.Evidence.Read(ctx, evidence.Query{SnapshotID: in.SnapshotID, ProductKey: in.ProductKey, Limit: 1})
		if err != nil {
			return Focus{}, err
		}
		if rows.Total == 0 {
			return Focus{}, errors.New("в снимке нет наблюдений для продукта")
		}
	}
	if in.ProductKey == "" || len(in.ProductKey) > 256 || strings.ContainsFunc(in.ProductKey, unicode.IsControl) {
		return Focus{}, errors.New("укажите ключ продукта")
	}
	if err := s.Decisions.SetFocus(ctx, in); err != nil {
		return Focus{}, err
	}
	return in, nil
}
