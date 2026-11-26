package product

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"context"
)

func (s Service) Rank(ctx context.Context, _ evidence.Empty) (decision.Priorities, error) {
	return s.Decisions.RankedDrafts(ctx, s.Evidence.CanReadLocal(ctx))
}
func (s Service) SetEstimate(ctx context.Context, in decision.EstimateInput) (decision.Record, error) {
	if _, err := s.Get(ctx, decision.GetInput{HypothesisID: in.HypothesisID}); err != nil {
		return decision.Record{}, err
	}
	out, err := s.Decisions.SetEstimate(ctx, in)
	return withCardURL(ctx, out), err
}
