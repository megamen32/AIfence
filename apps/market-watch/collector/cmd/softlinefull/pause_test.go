package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"context"
	"testing"
	"time"
)

func TestKnownHTTPStatusAndRetryCheckpoint(t *testing.T) {
	d := t.TempDir()
	u := "https://store.softline.ru/vendor/abc-7/"
	s := survey{out: d, dir: d, known: map[string]bool{u: true}, covered: map[string]softlinefamily.Product{}}
	err := s.scan(context.Background(), []string{u}, false, func(context.Context, catalog.Source, string) ([]byte, error) {
		return nil, &catalog.HTTPStatus{Code: 410}
	})
	if err != nil || s.covered[u].State != "unavailable" {
		t.Fatalf("gone: %s, %v", s.covered[u].State, err)
	}
	now := time.Now()
	after := now.Add(9 * time.Hour)
	err = s.scan(context.Background(), []string{u + "other"}, false, func(context.Context, catalog.Source, string) ([]byte, error) {
		return nil, &catalog.HTTPStatus{Code: 429, RetryAfter: after}
	})
	if err == nil || resumeAllowed(d, now) == nil || resumeAllowed(d, after.Add(time.Second)) != nil {
		t.Fatal("rate-limit deadline was not preserved")
	}
}
