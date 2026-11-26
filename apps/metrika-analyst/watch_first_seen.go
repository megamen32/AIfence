package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type watchDetection struct {
	DetectedAt     string `json:"detected_at,omitempty"`
	DetectedSource string `json:"detected_at_source,omitempty"`
}

type watchRecorder func(context.Context, string, []string) (map[string]watchDetection, error)

func firstSeenIn(tx agentsdk.DBTX, owner uuid.UUID) watchRecorder {
	return func(ctx context.Context, dataset string, ids []string) (map[string]watchDetection, error) {
		// A consistent lock order lets overlapping batches register concurrently.
		sort.Strings(ids)
		if _, err := tx.ExecContext(ctx, `INSERT INTO watchdog_first_seen
			(owner_id, dataset_id, signal_id, detected_at, detected_at_source)
			SELECT $1, $2, signal_id, statement_timestamp(), 'agent_observed'
			FROM unnest($3::text[]) AS signals(signal_id)
			ON CONFLICT (owner_id, dataset_id, signal_id) DO NOTHING`, owner, dataset, pq.Array(ids)); err != nil {
			return nil, fmt.Errorf("register watchdog detection: %w", err)
		}
		// A separate statement sees the winning row even if an insert raced.
		rows, err := tx.QueryContext(ctx, `SELECT signal_id, detected_at, detected_at_source
			FROM watchdog_first_seen WHERE owner_id=$1 AND dataset_id=$2 AND signal_id=ANY($3::text[])`,
			owner, dataset, pq.Array(ids))
		if err != nil {
			return nil, fmt.Errorf("read watchdog detection: %w", err)
		}
		defer rows.Close()
		found := make(map[string]watchDetection, len(ids))
		for rows.Next() {
			var id, source string
			var at time.Time
			if err := rows.Scan(&id, &at, &source); err != nil {
				return nil, err
			}
			found[id] = watchDetection{at.UTC().Format(time.RFC3339Nano), source}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := found[id]; !ok {
				return nil, fmt.Errorf("watchdog detection missing after registration")
			}
		}
		return found, nil
	}
}

func firstSeenInDB(db *agentsdk.AgentDB, owner uuid.UUID) watchRecorder {
	return func(ctx context.Context, dataset string, ids []string) (map[string]watchDetection, error) {
		var result map[string]watchDetection
		err := db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
			var err error
			result, err = firstSeenIn(tx, owner)(ctx, dataset, ids)
			return err
		})
		return result, err
	}
}

func attachSignalDetections(ctx context.Context, record watchRecorder, dataset string, batch SignalBatch) error {
	var ids []string
	for _, signal := range batch.Observations {
		if signal.Status == "observed" && signal.Value != nil && *signal.Value {
			ids = append(ids, signal.ID)
		}
	}
	if record == nil || len(ids) == 0 {
		return nil
	}
	found, err := record(ctx, dataset, ids)
	if err != nil {
		return err
	}
	for _, signal := range batch.Observations {
		if registered, ok := found[signal.ID]; ok {
			signal.Details["detected_at"] = registered.DetectedAt
			signal.Details["detected_at_source"] = registered.DetectedSource
		}
	}
	return nil
}

func signalDetection(signal Signal) watchDetection {
	return watchDetection{detailString(signal.Details, "detected_at"), detailString(signal.Details, "detected_at_source")}
}
