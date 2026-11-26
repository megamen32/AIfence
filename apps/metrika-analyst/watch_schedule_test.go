package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
	"github.com/google/uuid"
)

func watchdogCaller(ctx context.Context, human bool, surface, execution string) context.Context {
	caller := wire.Caller{
		Kind: "application", Access: wire.AccessAdmin,
		Origin: wire.CallerOrigin{Interface: surface, Execution: execution},
	}
	if human {
		caller.Kind, caller.Access = "user", wire.AccessUser
		caller.User = &wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
		caller.Initiator = caller.User
	}
	return testutil.WithCaller(ctx, caller)
}

func TestWatchdogScheduledCanary(t *testing.T) {
	register := testFirstSeen(t)
	s := privateWatch{canary: func(context.Context) bool { return true }, canaryRecord: register}
	var first watchdogDecision
	for _, human := range []bool{false, true} {
		surface := "schedule"
		if human {
			surface = "http"
		}
		ctx := watchdogCaller(t.Context(), human, surface, "job")
		got, err := s.runJob(ctx, agentsdk.JobContext{}, watchdogInput{Mode: "canary"})
		if err != nil || len(got.Decisions) != 3 || got.Decisions[0].Alert == nil || *got.Decisions[0].Alert ||
			got.Decisions[1].Alert == nil || !*got.Decisions[1].Alert || got.Decisions[2].Alert != nil || got.NextJobID != "" {
			t.Fatalf("unexpected canary result: %v", err)
		}
		alert := got.Decisions[1]
		if alert.SignalID == "" || alert.DetectedAt == "" || alert.DetectedSource != "agent_observed" ||
			alert.Magnitude == nil || got.Decisions[0].DetectedAt != "" || got.Decisions[2].DetectedAt != "" {
			t.Fatal("only confirmed alerts get a persisted detection")
		}
		if human && (first.watchDetection != alert.watchDetection || first.SignalID != alert.SignalID) {
			t.Fatal("manual and scheduled canary changed the first detection")
		}
		first = alert
	}
	app := watchdogCaller(t.Context(), false, "schedule", "job")
	if _, err := s.runJob(app, agentsdk.JobContext{}, watchdogInput{Mode: "owner"}); err == nil {
		t.Fatal("app cron gained owner authority")
	}
	request := watchdogCaller(t.Context(), true, "http", "request")
	if _, err := s.runJob(request, agentsdk.JobContext{}, watchdogInput{Mode: "canary"}); err == nil {
		t.Fatal("HTTP request bypassed job delivery")
	}
}

func TestWatchdogNativeManifestAndScope(t *testing.T) {
	a := newAgent()
	m := a.Manifest()
	if len(m.JobHandlers) != 1 || m.JobHandlers[0].Name != watchJobName || m.JobHandlers[0].MaxConcurrency != 1 ||
		len(m.JobCrons) != 1 || m.JobCrons[0].Slug != watchCron || m.JobCrons[0].HandlerName != watchJobName {
		t.Fatal("missing native job or daily cron")
	}
	ctx := watchdogCaller(t.Context(), true, "chat", "request")
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	privatePath := watchSnapshotPath(owner, uuid.New(), privateSeries)
	if _, err := a.ResolveFilePath(ctx, privatePath, agentsdk.FileOperationRead); err == nil {
		t.Fatal("chat caller could resolve a private native file")
	}
}

func TestWatchdogBrowserConsent(t *testing.T) {
	const origin = "http://metrika-analyst.localhost:42080"
	s := privateWatch{origin: func(context.Context) string { return origin }}
	ctx := watchdogCaller(t.Context(), true, "http", "request")
	body := "action=start&product_key=target&control_product_key=control&metric_key=orders_index"
	for _, tt := range []struct {
		name, origin string
		ctx          context.Context
		want         int
	}{
		{"cross origin", "https://other.example", ctx, http.StatusForbidden},
		{"missing consent", origin, ctx, http.StatusBadRequest},
		{"no human", origin, watchdogCaller(t.Context(), false, "application", "request"), http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/watchdog", bytes.NewBufferString(body)).WithContext(tt.ctx)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			if err := s.submit(w, r); err != nil || w.Code != tt.want {
				t.Fatalf("got status %d, want %d (%v)", w.Code, tt.want, err)
			}
		})
	}
}

func TestWatchdogPrivatePreparedPair(t *testing.T) {
	root := os.Getenv("AIFENCE_WATCH_CASE")
	if root == "" {
		t.Skip("private pair not configured")
	}
	var body bytes.Buffer
	upload := multipart.NewWriter(&body)
	for field, name := range map[string]string{"series": privateSeries, "context": privateContext} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal("prepared snapshot unreadable")
		}
		part, err := upload.CreateFormFile(field, name)
		if err != nil {
			t.Fatal("unable to encode test form")
		}
		if _, err := part.Write(b); err != nil {
			t.Fatal("unable to encode test body")
		}
	}
	if err := upload.Close(); err != nil {
		t.Fatal("unable to close test form")
	}
	r := httptest.NewRequest(http.MethodPost, "/watchdog", &body)
	r.Header.Set("Content-Type", upload.FormDataContentType())
	pair, err := readWatchPair(r)
	if err != nil || validateWatchPair(t.Context(), pair) != nil {
		t.Fatal("private pair did not pass local upload validation")
	}
}

func TestWatchdogCalendar(t *testing.T) {
	monday, err := time.Parse(time.RFC3339, "2023-03-06T06:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if p := latestClosedWeek(monday); p != (Period{"2023-02-27", "2023-03-05"}) {
		t.Fatalf("wrong completed Monday–Sunday window: %v", p)
	}
	if p := latestClosedWeek(monday.AddDate(0, 0, -1)); p.To != "2023-02-26" {
		t.Fatalf("Sunday reporting used an open week: %v", p)
	}
	if nextWatchAt(monday) != monday.AddDate(0, 0, 1) || nextWatchAt(monday.Add(-time.Minute)) != monday {
		t.Fatal("next delivery must be the first future occurrence at 06:00 UTC")
	}
}
