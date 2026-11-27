package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Fetch and parsing are independent pools. A bounded handoff prevents large
// bodies from consuming unbounded RAM; archived bodies survive a process crash.
const FetchWorkers = 16
const ParseWorkers = 4

type Downloaded struct {
	Body     []byte
	Evidence Evidence
	At       time.Time
	Err      error
	Skip     bool
}

func DownloadTask(ctx context.Context, s Source, url string, fetch Fetch) Downloaded {
	j, gen, kind := JobJournal(ctx)
	now := time.Now().UTC()
	if j != nil {
		allowed, e := j.MayRun(s.ID, url, gen, kind, now)
		if e != nil {
			return Downloaded{Err: e, At: now}
		}
		if !allowed {
			return Downloaded{Skip: true, At: now}
		}
		if e = j.Begin(s.ID, url, gen, kind); e != nil {
			return Downloaded{Err: &ArchiveFailure{e}, At: now}
		}
		raw, ev, ok, e := j.RetryBody(s.ID, url, gen, kind)
		if e != nil {
			return Downloaded{Err: e, At: now}
		}
		if ok {
			return Downloaded{Body: raw, Evidence: ev, At: ev.At}
		}
	}
	b, e := fetch(ctx, s, url)
	if e != nil {
		return Downloaded{Err: e, At: now}
	}
	if j != nil {
		ev, err := j.Latest(s.ID, url)
		if errors.Is(err, sql.ErrNoRows) {
			// Support existing fixture/offline fetch functions without inventing a
			// second network request. Actual ArchivedHTTP always wrote the receipt.
			ev, err = j.Import(s.ID, url, time.Now().UTC(), b)
		}
		if err != nil {
			return Downloaded{Err: fmt.Errorf("missing raw response receipt: %w", err), At: now}
		}
		raw, err := j.Raw(ev)
		if err != nil {
			return Downloaded{Err: err, At: now}
		}
		return Downloaded{Body: raw, Evidence: ev, At: ev.At}
	}
	return Downloaded{Body: b, At: time.Now().UTC()}
}
func RecordTask(ctx context.Context, s Source, url string, d Downloaded, resolved bool, parseErr error, fallback string) error {
	j, gen, kind := JobJournal(ctx)
	if j == nil || d.Skip {
		return nil
	}
	var admission *AdmissionPaused
	if errors.As(d.Err, &admission) || errors.Is(d.Err, context.Canceled) || errors.Is(parseErr, context.Canceled) {
		return nil
	}
	if resolved {
		return j.Resolve(s.ID, url, gen, kind, d.Evidence.ID)
	}
	e := d.Err
	if e == nil {
		e = parseErr
	}
	if d.Err != nil {
		fallback = "network"
	}
	typ, retry := FailureType(e, fallback)
	if typ == "" {
		typ = "parse_unclassified"
	}
	if typ == "rate_limit" || typ == "http_503" || typ == "forbidden" {
		retry = maxTime(retry, time.Now().Add(3*time.Hour))
	}
	if typ == "source_variant_not_listed" {
		// The source returned a real family but did not publish this exact URL
		// within its license data. Retain it as an unresolved source gap; avoid
		// repeated parsing and let the publisher's future state be checked later.
		retry = maxTime(retry, time.Now().Add(24*time.Hour))
	}

	detail := ""
	if e != nil {
		detail = e.Error()
	}
	return j.RecordFailure(s.ID, url, gen, kind, typ, detail, d.Evidence.ID, time.Now().UTC(), retry)
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func FatalControl(e error) bool {
	var h *HTTPStatus
	return errors.As(e, &h) && (h.Code == 403 || h.Code == 429 || h.Code == 503 || (h.Code >= 500 && h.RetryAfter.After(time.Now())))
}
func StopRequest(e error) bool {
	var archive *ArchiveFailure
	return FatalControl(e) || errors.As(e, &archive)
}

// Pipeline preserves the single checkpoint consumer. No goroutine writes
// catalog observations directly. A fatal control response cancels admission;
// already fetched results are drained so the longest Retry-After survives.
func Pipeline[J, R any](ctx context.Context, jobs []J, fetch func(context.Context, J) Downloaded, parse func(J, Downloaded) R, consume func(R) error) error {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	type pair struct {
		job  J
		data Downloaded
	}
	tasks := make(chan J)
	go func() {
		defer close(tasks)
		for _, job := range jobs {
			select {
			case <-work.Done():
				return
			case tasks <- job:
			}
		}
	}()
	fetched := make(chan pair, ParseWorkers)
	results := make(chan R, ParseWorkers)
	var fw, pw sync.WaitGroup
	for range FetchWorkers {
		fw.Add(1)
		go func() {
			defer fw.Done()
			for {
				select {
				case <-work.Done():
					return
				case job, ok := <-tasks:
					if !ok {
						return
					}
					if work.Err() != nil {
						return
					}
					d := fetch(work, job)
					if StopRequest(d.Err) {
						cancel()
					}
					fetched <- pair{job, d}
					if work.Err() != nil {
						return
					}
				}
			}
		}()
	}
	go func() { fw.Wait(); close(fetched) }()
	for range ParseWorkers {
		pw.Add(1)
		go func() {
			defer pw.Done()
			for p := range fetched {
				results <- parse(p.job, p.data)
			}
		}()
	}
	go func() { pw.Wait(); close(results) }()
	var first error
	for r := range results {
		if e := consume(r); e != nil {
			if first == nil {
				first = e
			}
			cancel()
		}
	}
	if first != nil {
		return first
	}
	return ctx.Err()
}
