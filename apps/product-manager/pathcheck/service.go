package pathcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Input struct{}
type Batch struct {
	Schema       string            `json:"schema_version"`
	Source       string            `json:"source"`
	AsOf         string            `json:"as_of"`
	Status       string            `json:"status"`
	Observations []json.RawMessage `json:"observations"`
	Warnings     []string          `json:"warnings"`
}
type Runner interface {
	Run(context.Context) (RunResult, error)
}
type RunResult struct {
	Evidence    []byte
	Screenshots map[string][]byte
	Cleanup     func()
}
type Store interface {
	Save(context.Context, string, []byte) (string, error)
}
type Service struct {
	runner Runner
	store  Store
	mu     sync.Mutex
}

func New(r Runner, store Store) *Service { return &Service{runner: r, store: store} }
func (s *Service) Check(ctx context.Context, _ Input) (Batch, error) {
	if !s.mu.TryLock() {
		return Batch{}, errors.New("purchase path check already running")
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result, err := s.runner.Run(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("purchase path check unavailable: %w", err)
	}
	if result.Cleanup != nil {
		defer result.Cleanup()
	}
	var batch Batch
	if err := json.Unmarshal(result.Evidence, &batch); err != nil || batch.Schema != "aifence.v1" || batch.Source != "browser" {
		return Batch{}, errors.New("invalid purchase path evidence")
	}
	for i, raw := range batch.Observations {
		var observation map[string]json.RawMessage
		if json.Unmarshal(raw, &observation) != nil {
			return Batch{}, errors.New("invalid purchase path observation")
		}
		var details map[string]json.RawMessage
		if v, ok := observation["details"]; ok && string(v) != "null" {
			if json.Unmarshal(v, &details) != nil {
				return Batch{}, errors.New("invalid purchase path details")
			}
			delete(details, "screenshot")
			delete(details, "screenshot_path")
			var device string
			_ = json.Unmarshal(observation["segment"], &device)
			if (device == "desktop" || device == "mobile") && len(result.Screenshots[device]) > 0 {
				path, err := s.store.Save(ctx, device, result.Screenshots[device])
				if err != nil {
					return Batch{}, errors.New("cannot save private screenshot")
				}
				details["screenshot_path"], _ = json.Marshal(path)
			}
			observation["details"], _ = json.Marshal(details)
		}
		batch.Observations[i], _ = json.Marshal(observation)
	}
	return batch, nil
}
