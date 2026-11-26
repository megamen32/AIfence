package decision

import (
	"context"
	"database/sql"
	"errors"

	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

type Focus struct {
	Synthetic    bool   `json:"synthetic,omitempty"`
	ProductKey   string `json:"product_key,omitempty"`
	SnapshotID   string `json:"snapshot_id,omitempty"`
	HypothesisID string `json:"hypothesis_id,omitempty"`
}

func (s *Service) GetFocus(ctx context.Context) (Focus, error) {
	who, err := owner(ctx)
	if err != nil {
		return Focus{}, err
	}
	row, err := s.q.PMReadFocus(ctx, who)
	if errors.Is(err, sql.ErrNoRows) {
		return Focus{}, nil
	}
	return Focus{ProductKey: row.ProductKey, SnapshotID: row.SnapshotID, HypothesisID: row.HypothesisID}, err
}

func (s *Service) SetFocus(ctx context.Context, focus Focus) error {
	who, err := owner(ctx)
	if err != nil {
		return err
	}
	return s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		n, err := db.New(tx).PMSetFocus(ctx, db.PMSetFocusParams{
			OwnerID: who, ProductKey: focus.ProductKey, SnapshotID: focus.SnapshotID, HypothesisID: focus.HypothesisID,
		})
		if err != nil || n == 0 {
			return err
		}
		return s.topic.Publish(ctx, tx, who)
	})
}
