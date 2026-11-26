package main

import (
	"agent/collector"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *survey) recordPause(status *catalog.HTTPStatus) error {
	until := status.RetryAfter
	if !until.After(time.Now()) {
		until = time.Now().Add(3 * time.Hour)
	}
	return save(filepath.Join(s.out, "retry-after.json"), until.UTC())
}
func resumeAllowed(out string, now time.Time) error {
	var until time.Time
	err := read(filepath.Join(out, "retry-after.json"), &until)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if now.Before(until) {
		return fmt.Errorf("source retry scheduled for %s", until.Format(time.RFC3339))
	}
	return nil
}
