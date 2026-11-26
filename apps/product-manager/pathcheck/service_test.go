package pathcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubRunner func(context.Context) (RunResult, error)

func (f stubRunner) Run(ctx context.Context) (RunResult, error) { return f(ctx) }

type stubStore func(context.Context, string, []byte) (string, error)

func (f stubStore) Save(ctx context.Context, device string, data []byte) (string, error) {
	return f(ctx, device, data)
}

func TestCheckStoresScreenshotBeforeCleanup(t *testing.T) {
	cleaned := false
	s := New(stubRunner(func(context.Context) (RunResult, error) {
		return RunResult{Evidence: []byte(`{"schema_version":"aifence.v1","source":"browser","observations":[{"segment":"desktop","details":{"screenshot":"desktop.png","stage":"cart"}}]}`), Screenshots: map[string][]byte{"desktop": []byte("fixture")}, Cleanup: func() { cleaned = true }}, nil
	}), stubStore(func(_ context.Context, device string, _ []byte) (string, error) {
		if cleaned || device != "desktop" {
			t.Fatal("invalid save order or device")
		}
		return "/apps/test/tmp/path-check/user-test/example/desktop.png", nil
	}))
	result, err := s.Check(t.Context(), Input{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(result.Observations[0])
	if !cleaned || !strings.Contains(text, `"screenshot_path":"/apps/test/`) || strings.Contains(text, `"screenshot":`) {
		t.Fatalf("unexpected result: %s", text)
	}
}
func TestCheckRejectsConcurrentRun(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := New(stubRunner(func(context.Context) (RunResult, error) {
		close(started)
		<-release
		return RunResult{}, errors.New("stopped")
	}), nil)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.Check(t.Context(), Input{}) }()
	<-started
	if _, err := s.Check(t.Context(), Input{}); err == nil {
		t.Error("concurrent run accepted")
	}
	close(release)
	<-done
}
func TestCheckRejectsInvalidEvidence(t *testing.T) {
	s := New(stubRunner(func(context.Context) (RunResult, error) {
		return RunResult{Evidence: []byte(`{"schema_version":"wrong"}`)}, nil
	}), nil)
	if _, err := s.Check(t.Context(), Input{}); err == nil {
		t.Fatal("invalid evidence accepted")
	}
}
