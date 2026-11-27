package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A source retains its existing writer lock. The database, not the order of
// sitemap files or an in-memory phase, decides which ready tasks run next.
type TaskSeed struct {
	URL, Kind, Lane string
	Priority        int
	Done            bool
	ErrorType       string
	At              time.Time
	EvidenceID      int64
}
type ReadyTask struct {
	URL, Kind, Lane string
	Priority        int
	EvidenceID      int64
	Retry           bool
}

func (j *Journal) InitSchedule() error {
	// An additive migration, safe with independent source writer processes.
	if _, e := j.db.Exec("ALTER TABLE crawl_tasks ADD COLUMN lane TEXT NOT NULL DEFAULT ''"); e != nil && !strings.Contains(e.Error(), "duplicate column name") {
		return e
	}
	_, e := j.db.Exec(`
 CREATE INDEX IF NOT EXISTS task_schedule ON crawl_tasks(source,generation,kind,state,priority DESC,url);
 CREATE INDEX IF NOT EXISTS task_schedule_lane ON crawl_tasks(source,generation,kind,lane,state,priority DESC,url);
 CREATE INDEX IF NOT EXISTS response_latest_ok ON http_responses(source,status,url,id DESC);
 CREATE INDEX IF NOT EXISTS fail_schedule ON failed_tasks(source,generation,kind,state,next_attempt);
 `)
	return e
}

// Reconcile the task ledger against durable, exact checkpoints. Do not reset
// attempts, future backoff or a blocked source just because we restarted.
func (j *Journal) SeedPlan(source, generation string, seeds []TaskSeed) error {
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UnixNano()
	task, e := tx.Prepare(`INSERT INTO crawl_tasks(source,url,kind,generation,priority,state,updated_at,evidence_id,lane) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(source,url,kind,generation) DO UPDATE SET priority=excluded.priority,lane=excluded.lane,
 state=CASE WHEN excluded.state='done' THEN 'done' WHEN crawl_tasks.state IN ('running','done') THEN 'queued' ELSE crawl_tasks.state END,
 evidence_id=excluded.evidence_id`)
	if e != nil {
		return e
	}
	defer task.Close()
	fail, e := tx.Prepare(`INSERT INTO failed_tasks(source,url,kind,generation,error_type,attempts,next_attempt,evidence_id,state,detail,updated_at) VALUES(?,?,?,?,?,1,?,?,?,'migrated from source checkpoint',?)
 ON CONFLICT(source,url,kind,generation) DO UPDATE SET
 evidence_id=CASE WHEN failed_tasks.evidence_id=0 AND excluded.evidence_id>0 THEN excluded.evidence_id ELSE failed_tasks.evidence_id END`)
	if e != nil {
		return e
	}
	defer fail.Close()
	del, e := tx.Prepare("DELETE FROM failed_tasks WHERE source=? AND url=? AND kind=? AND generation=?")
	if e != nil {
		return e
	}
	defer del.Close()
	for _, s := range seeds {
		if s.URL == "" || s.Kind == "" {
			return fmt.Errorf("empty task identity")
		}
		state := "queued"
		if s.Done {
			state = "done"
		}
		if _, e = task.Exec(source, s.URL, s.Kind, generation, s.Priority, state, now, s.EvidenceID, s.Lane); e != nil {
			return e
		}
		if s.Done {
			if _, e = del.Exec(source, s.URL, s.Kind, generation); e != nil {
				return e
			}
			continue
		}
		if s.ErrorType != "" {
			fs := "retry"
			if s.ErrorType == "robots_excluded" || s.ErrorType == "forbidden" {
				fs = "blocked"
			}
			next := s.At.UnixNano()
			if s.At.IsZero() {
				next = now
			}
			if _, e = fail.Exec(source, s.URL, s.Kind, generation, s.ErrorType, next, s.EvidenceID, fs, now); e != nil {
				return e
			}
		}
	}
	// Migrate legacy parse retries only when an actual successful response is
	// retained. attempts=0 is a deliberate requeue by an accepted rule.
	if _, e = tx.Exec(`UPDATE failed_tasks SET state='repair',next_attempt=0
 WHERE source=? AND generation=? AND state IN ('retry','exhausted') AND attempts>0
 AND error_type LIKE 'parse_%' AND evidence_id>0
 AND EXISTS(SELECT 1 FROM http_responses r WHERE r.id=failed_tasks.evidence_id AND r.status=200 AND r.source=failed_tasks.source AND r.url=failed_tasks.url)`, source, generation); e != nil {
		return e
	}
	// A failure row is authoritative for its retry state, including old tasks
	// migrated by previous versions without a corresponding ledger row.
	if _, e = tx.Exec(`INSERT INTO crawl_tasks(source,url,kind,generation,priority,state,updated_at,evidence_id)
 SELECT source,url,kind,generation,0,state,updated_at,evidence_id FROM failed_tasks WHERE source=? AND generation=?
 ON CONFLICT(source,url,kind,generation) DO UPDATE SET state=excluded.state,evidence_id=excluded.evidence_id`, source, generation); e != nil {
		return e
	}
	return tx.Commit()
}

// Fetch a bounded, fair window of work, re-evaluated after every window.
// Each family/product lane gets a share; local reparses never wait for the
// full sitemap. Old interrupted running rows are eligible under the writer lock.
func (j *Journal) Ready(source, generation string, kinds []string, limit int, now time.Time) ([]ReadyTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	type lane struct{ kind, name string }
	var lanes []lane
	for _, kind := range kinds {
		if kind == "page" {
			lanes = append(lanes, lane{kind, "family"}, lane{kind, "product"}, lane{kind, ""})
		} else {
			lanes = append(lanes, lane{kind, ""})
		}
	}
	var out []ReadyTask
	seen := map[string]bool{}
	pull := func(l lane, mode string, n int) error {
		if n <= 0 || len(out) >= limit {
			return nil
		}
		base := `SELECT t.url,t.kind,t.priority,
 CASE WHEN f.url IS NULL THEN t.evidence_id WHEN f.state='retry' AND f.error_type LIKE 'parse_%' THEN f.evidence_id ELSE 0 END,
 CASE WHEN f.state='retry' THEN 1 ELSE 0 END
 FROM crawl_tasks t LEFT JOIN failed_tasks f ON f.source=t.source AND f.url=t.url AND f.kind=t.kind AND f.generation=t.generation
 WHERE t.source=? AND t.generation=? AND t.kind=? AND t.lane=? AND t.state!='done' AND
 ((f.url IS NULL AND t.state IN ('queued','running','retry')) OR (f.state='retry' AND f.next_attempt<=?))`
		switch mode {
		case "local":
			base += " AND ((f.url IS NULL AND t.evidence_id>0) OR (f.state='retry' AND f.error_type LIKE 'parse_%' AND f.evidence_id>0))"
		case "fresh":
			base += " AND f.url IS NULL AND t.evidence_id=0"
		case "retry":
			base += " AND f.state='retry' AND NOT (f.error_type LIKE 'parse_%' AND f.evidence_id>0)"
		}
		base += " ORDER BY t.priority DESC,COALESCE(f.next_attempt,t.updated_at),t.url LIMIT ?"
		rows, e := j.db.Query(base, source, generation, l.kind, l.name, now.UnixNano(), limit+n)
		if e != nil {
			return e
		}
		defer rows.Close()
		added := 0
		for rows.Next() {
			var t ReadyTask
			var retry int
			if e = rows.Scan(&t.URL, &t.Kind, &t.Priority, &t.EvidenceID, &retry); e != nil {
				return e
			}
			key := t.Kind + "\x00" + t.URL
			if seen[key] {
				continue
			}
			seen[key] = true
			t.Retry = retry != 0
			out = append(out, t)
			added++
			if added >= n || len(out) >= limit {
				break
			}
		}
		return rows.Err()
	}
	per := max(1, limit/(len(lanes)*3))
	for len(out) < limit {
		before := len(out)
		for _, mode := range []string{"local", "fresh", "retry"} {
			for _, l := range lanes {
				if e := pull(l, mode, per); e != nil {
					return nil, e
				}
			}
		}
		if len(out) == before {
			break
		}
	}
	return out, nil
}

func (j *Journal) NextScheduleDue(source, generation string) (time.Time, bool, error) {
	var ns sql.NullInt64
	e := j.db.QueryRow("SELECT MIN(next_attempt) FROM failed_tasks WHERE source=? AND generation=? AND state='retry'", source, generation).Scan(&ns)
	if e != nil || !ns.Valid {
		return time.Time{}, false, e
	}
	return time.Unix(0, ns.Int64), true, nil
}
func (j *Journal) ArchiveIndex(source string) (map[string]Evidence, error) {
	rows, e := j.db.Query("SELECT "+evidenceCols+" FROM http_responses WHERE source=? AND status=200 ORDER BY id", source)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[string]Evidence{}
	for rows.Next() {
		ev, e := readEvidenceRow(rows)
		if e != nil {
			return nil, e
		}
		m[ev.URL] = ev
	}
	return m, rows.Err()
}

// Claimed task with a recorded body can resume parsing without touching HTTP.
// A network retry without a body uses the existing bounded host client.
func DownloadReady(ctx context.Context, s Source, t ReadyTask, fetch Fetch) Downloaded {
	j, gen, kind := JobJournal(ctx)
	if j != nil && t.EvidenceID > 0 {
		allowed, e := j.MayRun(s.ID, t.URL, gen, kind, time.Now())
		if e != nil {
			return Downloaded{Err: &ArchiveFailure{e}, At: time.Now()}
		}
		if !allowed {
			return Downloaded{Skip: true}
		}
		ev, e := j.Evidence(t.EvidenceID)
		if e != nil {
			return Downloaded{Err: &ArchiveFailure{e}, At: time.Now()}
		}
		if ev.Source != s.ID || ev.URL != t.URL || ev.Status != 200 {
			return Downloaded{Err: &ArchiveFailure{fmt.Errorf("incorrect retained task evidence")}, At: time.Now()}
		}
		if e = j.Begin(s.ID, t.URL, gen, kind); e != nil {
			return Downloaded{Err: &ArchiveFailure{e}, At: time.Now()}
		}
		body, e := j.Raw(ev)
		if e != nil {
			return Downloaded{Err: &ArchiveFailure{e}, At: ev.At}
		}
		return Downloaded{Body: body, Evidence: ev, At: ev.At}
	}
	return DownloadTask(ctx, s, t.URL, fetch)
}
func (j *Journal) ScheduleCounts(source, generation string) (map[string]int, error) {
	rows, e := j.db.Query("SELECT state,COUNT(*) FROM crawl_tasks WHERE source=? AND generation=? GROUP BY state", source, generation)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if e = rows.Scan(&state, &n); e != nil {
			return nil, e
		}
		m[state] = n
	}
	return m, rows.Err()
}
func WaitingForRule(errorType string, evidenceID int64) bool {
	return strings.HasPrefix(errorType, "parse_") && evidenceID > 0
}
func WaitForWork(ctx context.Context, next time.Time) error {
	d := time.Minute
	if !next.IsZero() {
		d = min(d, max(time.Second, time.Until(next)))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func HasWork(m map[string]int) bool { return m["queued"]+m["running"]+m["retry"] > 0 }
func IsMissing(err error) bool      { return errors.Is(err, sql.ErrNoRows) }
