package trends

import "context"

type Tools struct{ service *Service }

func NewTools(service *Service) *Tools { return &Tools{service: service} }
func (t *Tools) Compare(ctx context.Context, q Query) (Result, error) {
	return t.service.Compare(ctx, q)
}
func (t *Tools) Regional(ctx context.Context, q Query) (Result, error) {
	return t.service.Regional(ctx, q)
}
func (t *Tools) Related(ctx context.Context, q Query) (Result, error) {
	return t.service.Related(ctx, q)
}
