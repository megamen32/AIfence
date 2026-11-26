package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

const (
	watchJobName          = "watchdog_run"
	watchCron             = "watchdog_daily"
	watchAtUTC            = 6
	syntheticWatchProduct = "kaspersky_small_office_security"
	syntheticWatchControl = "dr_web_desktop_security_suite"
)

type watchdogInput struct {
	Mode string `json:"mode" enum:"canary,owner"`
}

type watchdogDecision struct {
	WeekFrom string `json:"week_from"`
	Alert    *bool  `json:"alert"`
	Reason   string `json:"reason"`
}

type watchdogResult struct {
	Status    string             `json:"status"`
	Decisions []watchdogDecision `json:"decisions"`
	NextJobID string             `json:"next_job_id,omitempty"`
}

type privateWatch struct {
	agent       *agentsdk.Agent
	db          *agentsdk.AgentDB
	job         *agentsdk.JobHandle[watchdogInput, watchdogResult]
	origin      func(context.Context) string
	canary      func(context.Context) bool
	privateHTTP chan struct{}
}

func registerWatchSchedule(a *agentsdk.Agent) {
	origin := a.RegisterEnvVar(&agentsdk.EnvVar{
		Slug: "watchdog_closed_origin", Description: "Browser origin of the closed Airlock app. Empty disables private watchdog access.",
	})
	canary := a.RegisterEnvVar(&agentsdk.EnvVar{
		Slug: "watchdog_canary_enabled", Description: "Run the native cron against embedded synthetic evidence", Default: "false", Pattern: "^(true|false)$",
	})
	s := &privateWatch{
		agent: a, db: a.DB(), privateHTTP: make(chan struct{}, 1),
		origin: func(ctx context.Context) string { v, _ := origin.Get(ctx); return v },
		canary: func(ctx context.Context) bool { v, _ := canary.Get(ctx); return v == "true" },
	}
	a.RegisterDirectory(privateWatchDir, agentsdk.DirectoryOpts{
		Read: agentsdk.AccessInternal, Write: agentsdk.AccessInternal, List: agentsdk.AccessInternal,
		Scope: agentsdk.ScopeUser, Description: "Private watchdog inputs for native execution only",
	})
	s.job = agentsdk.RegisterJob(a, &agentsdk.Job[watchdogInput, watchdogResult]{
		Name: watchJobName, Version: 1, Description: "Evaluate the weekly order watchdog without a model.",
		Timeout: 30 * time.Second, MaxAttempts: 3, MaxConcurrency: 1, Handler: s.runJob,
	})
	s.job.Cron(&agentsdk.JobCron[watchdogInput]{
		Slug: watchCron, Schedule: "CRON_TZ=UTC 0 6 * * *", Input: watchdogInput{Mode: "canary"},
		Description: "Daily detector canary on embedded synthetic evidence. Caller-owned schedules start from the closed watchdog page.",
	})
	s.registerRoutes(a)
	s.registerBlindTools(a)
}

func watchSummary(batch SignalBatch) watchdogResult {
	out := watchdogResult{Status: batch.Status, Decisions: []watchdogDecision{}}
	for _, item := range batch.Observations {
		reason, _ := item.Details["reason"].(string)
		out.Decisions = append(out.Decisions, watchdogDecision{WeekFrom: item.Period.From, Alert: item.Value, Reason: reason})
	}
	return out
}

func (s *privateWatch) runJob(ctx context.Context, job agentsdk.JobContext, input watchdogInput) (watchdogResult, error) {
	caller := agentsdk.CallerFromContext(ctx)
	switch input.Mode {
	case "canary":
		scheduled := caller.Kind() == agentsdk.CallerApplication && caller.Origin().Interface == agentsdk.InterfaceSchedule
		_, manual := watchOwner(ctx, agentsdk.ExecutionJob)
		if caller.Origin().Execution != agentsdk.ExecutionJob || (!scheduled && manual != nil) {
			return watchdogResult{}, errors.New("watchdog canary requires an official job delivery")
		}
		if !s.canary(ctx) {
			return watchdogResult{Status: "disabled", Decisions: []watchdogDecision{}}, nil
		}
		w := Watch{
			series: Service{
				read: func(_ context.Context, path string) ([]byte, error) {
					if b, ok := embeddedEvidence(path); ok {
						return b, nil
					}
					return nil, errors.New("embedded evidence unavailable")
				},
				config: func(context.Context) Settings { return Settings{} },
			},
			contextPath: func(context.Context) string { return "" },
		}
		batch, err := w.Replay(ctx, SignalInput{
			ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
			From: "2023-02-13", To: "2023-03-05", AsOf: "2023-03-03",
			SourcePath: "fixtures/synthetic-watch.json", ContextPath: "fixtures/synthetic-watch-context.json",
		})
		return watchSummary(batch), err
	case "owner":
		owner, err := watchOwner(ctx, agentsdk.ExecutionJob)
		if err != nil {
			return watchdogResult{}, err
		}
		if !closedOrigin(s.origin(ctx)) {
			return watchdogResult{Status: "disabled", Decisions: []watchdogDecision{}}, nil
		}
		return s.runOwner(ctx, job, owner)
	default:
		return watchdogResult{}, errors.New("unknown watchdog job mode")
	}
}

func watchOwner(ctx context.Context, execution agentsdk.ExecutionKind) (uuid.UUID, error) {
	caller := agentsdk.CallerFromContext(ctx)
	user, ok := caller.User()
	if !ok || caller.Kind() != agentsdk.CallerUser || !user.PlatformMember ||
		caller.Origin().Execution != execution || (caller.Access() != agentsdk.AccessUser && caller.Access() != agentsdk.AccessAdmin) {
		return uuid.Nil, errors.New("watchdog requires the authorized human owner")
	}
	id, err := uuid.Parse(user.ID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid Airlock user identity")
	}
	return id, nil
}

func latestClosedWeek(at time.Time) Period {
	through := at.UTC().AddDate(0, 0, -1)
	start := monday(through)
	if through.Weekday() != time.Sunday {
		start = start.AddDate(0, 0, -7)
	}
	return week(start)
}

func nextWatchAt(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), watchAtUTC, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *privateWatch) runOwner(ctx context.Context, job agentsdk.JobContext, owner uuid.UUID) (watchdogResult, error) {
	out := watchdogResult{Status: "inactive", Decisions: []watchdogDecision{}}
	err := s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		var snapshot uuid.UUID
		var pending, previous sql.NullString
		var previousOutput []byte
		var product, control, metric, segment sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT snapshot_id, next_job_id::text, last_job_id::text, last_result,
		    product_key, control_product_key, metric_key, segment
		    FROM watchdog_owners WHERE owner_id=$1 FOR UPDATE`, owner).
			Scan(&snapshot, &pending, &previous, &previousOutput, &product, &control, &metric, &segment)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if previous.String == job.ID {
			return json.Unmarshal(previousOutput, &out)
		}
		if pending.String != job.ID {
			return nil
		}

		now := time.Now()
		at := now
		if job.ScheduledAt != nil {
			at = *job.ScheduledAt
		}
		period := latestClosedWeek(at)
		input := SignalInput{
			ProductKey: product.String, ControlProductKey: control.String, MetricKey: metric.String, Segment: segment.String,
			From: period.From, To: period.To, AsOf: period.To,
			SourcePath: privateSeries, ContextPath: privateContext,
		}
		w := s.snapshotWatch(owner, snapshot)
		batch, runErr := w.Detect(ctx, input)
		if runErr != nil {
			out.Status = "source_unavailable"
		} else {
			out = watchSummary(batch)
		}
		jobID, err := uuid.Parse(job.ID)
		if err != nil {
			return fmt.Errorf("invalid platform job ID: %w", err)
		}
		successor := uuid.NewSHA1(jobID, []byte("next-daily-watchdog")).String()
		if _, err := s.job.EnqueueAt(ctx, successor, nextWatchAt(now), watchdogInput{Mode: "owner"}); err != nil {
			// A previous attempt may have accepted the same next occurrence before its DB commit.
			if _, getErr := s.job.Get(ctx, successor); getErr != nil {
				return err
			}
		}
		out.NextJobID = successor
		receipt, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE watchdog_owners SET next_job_id=$2, last_job_id=$3, last_result=$4
		    WHERE owner_id=$1`, owner, successor, job.ID, receipt)
		return err
	})
	return out, err
}
