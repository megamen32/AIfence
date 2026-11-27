package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type deliverySettingStub struct {
	value string
	err   error
}

func (s deliverySettingStub) Get(context.Context) (string, error) { return s.value, s.err }

func TestReadScheduleSettingsUsesOwnerSettings(t *testing.T) {
	got := readScheduleSettings(context.Background(), deliverySettingStub{value: "6"}, deliverySettingStub{value: "14"})
	if got.syncEvery != 6*time.Hour {
		t.Fatalf("sync interval = %s, want 6h", got.syncEvery)
	}
	if got.morningHourUTC != 9 {
		t.Fatalf("morning UTC hour = %d, want 9 for 14:00 UTC+5", got.morningHourUTC)
	}
}

func TestReadScheduleSettingsFallsBackToCurrentDefaults(t *testing.T) {
	got := readScheduleSettings(context.Background(), deliverySettingStub{value: "0"}, deliverySettingStub{value: "25"})
	if got.syncEvery != syncEvery || got.morningHourUTC != morningHourUTC {
		t.Fatalf("invalid settings = %+v, want defaults %s/%d", got, syncEvery, morningHourUTC)
	}

	got = readScheduleSettings(context.Background(), deliverySettingStub{err: errors.New("unavailable")}, deliverySettingStub{err: errors.New("unavailable")})
	if got.syncEvery != syncEvery || got.morningHourUTC != morningHourUTC {
		t.Fatalf("unavailable settings = %+v, want defaults %s/%d", got, syncEvery, morningHourUTC)
	}
}

func TestNextMorningUsesConfiguredHour(t *testing.T) {
	now := time.Date(2026, time.November, 27, 8, 30, 0, 0, time.UTC)
	got := nextMorning(now, 9)
	want := time.Date(2026, time.November, 27, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("next morning = %s, want %s", got, want)
	}

	got = nextMorning(time.Date(2026, time.November, 27, 10, 0, 0, 0, time.UTC), 9)
	want = time.Date(2026, time.November, 28, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("next-day morning = %s, want %s", got, want)
	}
}
