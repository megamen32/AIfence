package market

import (
	catalog "agent/collector"
	"agent/internal/db"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/airlockrun/agentsdk"
)

const nativeFetchWorkers = 16
const nativeParseWorkers = 4
const nativeWindowSize = 120

type nativeArchive struct {
	s       *Service
	ctx     context.Context
	a       AdaptiveStore
	holdout map[string]bool
	mu      sync.Mutex
	latest  map[string]AdapterReceipt
}

func (n *nativeArchive) capture(_ context.Context, source, requested, actual string, status int, location string, at time.Time, body []byte) error {
	if source != n.a.ID || !catalog.Allowed(n.a.source(), requested) || !catalog.Allowed(n.a.source(), actual) {
		return fmt.Errorf("archive identity mismatch")
	}
	hash := sampleHash(body)
	path := adapterPath(n.a.ID) + "/raw/" + hash + ".gz"
	// Immutable address derives from the exact raw body. Airlock Storage
	// deduplicates content; never keep a body only in an ephemeral worker.
	if _, e := n.s.store.ReadFile(n.ctx, path); errors.Is(e, agentsdk.ErrNotFound) {
		var compressed bytes.Buffer
		z, e := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		if e != nil {
			return e
		}
		if _, e = z.Write(body); e != nil {
			_ = z.Close()
			return e
		}
		if e = z.Close(); e != nil {
			return e
		}
		if _, e = n.s.store.WriteFile(n.ctx, path, bytes.NewReader(compressed.Bytes()), "application/gzip"); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	hold, ok := n.holdout[requested]
	if !ok {
		hold = sampleHash([]byte(requested))[0]%3 == 0
	}
	r := AdapterReceipt{URL: requested, At: at, Hash: hash, Holdout: hold, HTML: status == 200 && isAdapterHTML(actual, body), HTTPStatus: status, ResponseURL: actual, Location: location, Bytes: int64(len(body)), ArchiveState: "stored"}
	raw, e := json.Marshal([]AdapterReceipt{r})
	if e != nil {
		return e
	}
	lease, ok := n.ctx.Value(adaptiveLeaseKey{}).(adaptiveLease)
	if !ok || lease.ID != n.a.ID {
		return fmt.Errorf("archive requires owned lease")
	}
	e = n.s.conn.Transaction(n.ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		if _, e := q.CheckAdaptiveLease(n.ctx, db.CheckAdaptiveLeaseParams{StoreID: lease.ID, JobID: lease.Job, Attempt: lease.Attempt}); e != nil {
			return e
		}
		return q.InsertAdaptiveResponses(n.ctx, db.InsertAdaptiveResponsesParams{StoreID: n.a.ID, Receipts: raw})
	})
	if e != nil {
		return e
	}
	if status == 200 {
		n.mu.Lock()
		n.latest[requested] = r
		n.mu.Unlock()
	}
	return nil
}
func (n *nativeArchive) receipt(u string) (AdapterReceipt, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	r, ok := n.latest[u]
	return r, ok
}

type nativeDownloaded struct {
	Task   AdapterTask
	Sample catalog.AdapterSample
	Err    error
}
type nativeParsed struct {
	Download nativeDownloaded
	Result   catalog.AdapterResult
	Links    []string
	Template string
	ParseErr error
}

// All goroutines belong to the Job and are joined before it returns.
// Fetch and parse are separate bounded stages; one consumer owns state.
func nativePipeline(ctx context.Context, tasks []AdapterTask, download func(context.Context, AdapterTask) nativeDownloaded, parse func(nativeDownloaded) nativeParsed, consume func(nativeParsed) error) error {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan AdapterTask)
	downloaded := make(chan nativeDownloaded, nativeParseWorkers)
	parsed := make(chan nativeParsed, nativeParseWorkers)
	go func() {
		defer close(jobs)
		for _, t := range tasks {
			select {
			case <-work.Done():
				return
			case jobs <- t:
			}
		}
	}()
	var fetchWG, parseWG sync.WaitGroup
	for range nativeFetchWorkers {
		fetchWG.Add(1)
		go func() {
			defer fetchWG.Done()
			for t := range jobs {
				if work.Err() != nil {
					return
				}
				d := download(work, t)
				// A control response stops admission in the shared HTTP client. Drain
				// already downloaded evidence through the single state writer.
				var he *catalog.HTTPStatus
				var af *catalog.ArchiveFailure
				if errors.As(d.Err, &he) && (he.Code == 403 || he.Code == 429 || he.Code == 503 || he.RetryAfter.After(time.Now())) || errors.As(d.Err, &af) || (d.Err != nil && strings.Contains(d.Err.Error(), "robots unavailable")) {
					cancel()
				}
				downloaded <- d
			}
		}()
	}
	go func() { fetchWG.Wait(); close(downloaded) }()
	for range nativeParseWorkers {
		parseWG.Add(1)
		go func() {
			defer parseWG.Done()
			for d := range downloaded {
				parsed <- parse(d)
			}
		}()
	}
	go func() { parseWG.Wait(); close(parsed) }()
	var first error
	for v := range parsed {
		if first != nil {
			continue
		}
		if e := consume(v); e != nil {
			first = e
			cancel()
		}
	}
	return first
}
func (s *Service) commitNativeTasks(ctx context.Context, a *AdaptiveStore, tasks []AdapterTask, offers []catalog.Offer) error {
	lease, ok := ctx.Value(adaptiveLeaseKey{}).(adaptiveLease)
	if !ok || lease.ID != a.ID {
		return fmt.Errorf("native write requires owned lease")
	}
	raw, e := json.Marshal(tasks)
	if e != nil {
		return e
	}
	quotes, e := json.Marshal(offers)
	if e != nil {
		return e
	}
	e = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		if _, e := q.CheckAdaptiveLease(ctx, db.CheckAdaptiveLeaseParams{StoreID: lease.ID, JobID: lease.Job, Attempt: lease.Attempt}); e != nil {
			return e
		}
		if e := q.UpsertAdaptiveTasks(ctx, db.UpsertAdaptiveTasksParams{StoreID: a.ID, Tasks: raw}); e != nil {
			return e
		}
		if len(offers) > 0 {
			return q.ImportObservations(ctx, quotes)
		}
		return nil
	})
	if e == nil {
		if a.savedTasks == nil {
			a.savedTasks = map[string]string{}
		}
		for _, t := range tasks {
			b, _ := json.Marshal(t)
			a.savedTasks[t.URL] = string(b)
		}
	}
	return e
}
func (s *Service) refreshAdaptiveNative(ctx context.Context, src catalog.Source, a AdaptiveStore, at time.Time) catalog.Run {
	r := catalog.Run{Source: src.ID, Mode: "native_parallel", Status: "partial", Offers: []catalog.Offer{}}
	ready, e := s.dueAdaptive(ctx, a, at)
	if e != nil {
		r.Errors = []string{e.Error()}
		return r
	}
	r.CandidatePages = len(ready)
	// Read persisted response receipts once. Previously downloaded pages are
	// reused after interrupted execution, not downloaded a second time.
	latest := map[string]AdapterReceipt{}
	holdout := map[string]bool{}
	for _, v := range a.Samples {
		holdout[v.URL] = v.Holdout
		if !v.HTML {
			continue
		}
		if old, ok := latest[v.URL]; !ok || v.At.After(old.At) {
			latest[v.URL] = v
		}
	}
	snapshot := a
	archive := &nativeArchive{s: s, ctx: ctx, a: snapshot, holdout: holdout, latest: map[string]AdapterReceipt{}}
	fetch := catalog.NewPublicArchivedHTTP(archive.capture).Fetch
	window, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	e = nativePipeline(window, ready, func(work context.Context, t AdapterTask) nativeDownloaded {
		d := nativeDownloaded{Task: t}
		if old, ok := latest[t.URL]; ok && old.Hash != t.Hash {
			d.Sample, d.Err = s.loadSample(ctx, snapshot, old)
			if d.Err != nil {
				d.Err = &catalog.ArchiveFailure{Err: d.Err}
			}
			return d
		}
		body, err := fetch(work, src, t.URL)
		d.Err = err
		if err != nil {
			return d
		}
		receipt, ok := archive.receipt(t.URL)
		if !ok || receipt.Hash != sampleHash(body) {
			d.Err = &catalog.ArchiveFailure{Err: fmt.Errorf("response missing archive receipt")}
			return d
		}
		d.Sample = catalog.AdapterSample{URL: t.URL, Body: body, CapturedAt: receipt.At, Holdout: receipt.Holdout}
		return d
	}, func(d nativeDownloaded) nativeParsed {
		p := nativeParsed{Download: d}
		if d.Err != nil {
			return p
		}
		p.Result, p.ParseErr = catalog.ParseAdapter(src, *src.Adaptive, d.Task.URL, d.Sample.Body, d.Sample.CapturedAt)
		p.Links = catalog.AdapterLinks(src, *src.Adaptive, d.Task.URL, d.Sample.Body)
		p.Template = catalog.AdapterFingerprint(d.Task.URL, d.Sample.Body)
		return p
	}, func(p nativeParsed) error {
		d, t := p.Download, p.Download.Task
		if d.Err != nil {
			var admission *catalog.AdmissionPaused
			if errors.As(d.Err, &admission) || errors.Is(d.Err, context.Canceled) || (window.Err() != nil && errors.Is(d.Err, context.DeadlineExceeded)) {
				return nil
			}
			oldPause := a.PauseUntil
			var he *catalog.HTTPStatus
			control := errors.As(d.Err, &he) && (he.Code == 403 || he.Code == 429 || he.Code == 503 || he.RetryAfter.After(s.now()))
			if control {
				a.PauseUntil = maxTimeAdaptive(a.PauseUntil, maxTimeAdaptive(he.RetryAfter, s.now().Add(3*time.Hour)))
			}
			if strings.Contains(d.Err.Error(), "robots unavailable") {
				a.PauseUntil = maxTimeAdaptive(a.PauseUntil, s.now().Add(3*time.Hour))
				if e := s.saveAdaptive(ctx, &a); e != nil {
					return e
				}
				r.Errors = append(r.Errors, "source robots policy unavailable")
				return nil
			}
			var archiveFailure *catalog.ArchiveFailure
			if errors.As(d.Err, &archiveFailure) {
				if a.PauseUntil.After(oldPause) {
					if e := s.saveAdaptive(ctx, &a); e != nil {
						return e
					}
				}
				return d.Err
			}
			pause := a.PauseUntil
			a.taskFailure(t.URL, d.Err, s.now())
			a.PauseUntil = maxTimeAdaptive(a.PauseUntil, pause)
			if e := s.commitNativeTasks(ctx, &a, []AdapterTask{a.Tasks[t.URL]}, nil); e != nil {
				return e
			}
			if a.PauseUntil.After(oldPause) {
				if e := s.saveAdaptive(ctx, &a); e != nil {
					return e
				}
			}
			r.Errors = append(r.Errors, t.URL+": "+d.Err.Error())
			r.Pages++
			return nil
		}
		r.Pages++
		t.Hash = sampleHash(d.Sample.Body)
		t.RuleVersion = a.ActiveVersion
		t.Template = p.Template
		t.Attempts = 0
		t.Error = ""
		if p.ParseErr != nil || (len(p.Result.Variants) == 0 && len(p.Links) == 0) || p.Result.Unknown > 0 {
			t.State = "repair"
			t.Error = "unresolved template"
			if p.ParseErr != nil {
				t.Error = p.ParseErr.Error()
			}
		} else {
			t.State = "done"
			t.Next = s.now().Add(3 * time.Hour)
		}
		a.Tasks[t.URL] = t
		changed := []AdapterTask{t}
		for _, u := range p.Links {
			if _, ok := a.Tasks[u]; ok {
				continue
			}
			a.enqueue(u)
			if next, ok := a.Tasks[u]; ok {
				changed = append(changed, next)
			}
		}
		var offers []catalog.Offer
		if p.ParseErr == nil {
			offers = p.Result.Offers
			r.Offers = append(r.Offers, offers...)
		}
		return s.commitNativeTasks(ctx, &a, changed, offers)
	})
	if e != nil {
		r.Errors = append(r.Errors, e.Error())
		return r
	}
	// Reload evidence and failure state before attempting a new rule. Identical
	// template/evidence pairs stay in repair, not five repeated HTTP requests.
	a, e = s.readAdaptive(ctx, a.ID)
	if e != nil {
		r.Errors = append(r.Errors, e.Error())
		return r
	}
	clusters := map[string]int{}
	for _, t := range a.Tasks {
		if t.State == "repair" {
			clusters[t.Template]++
		}
	}
	repeated := false
	for _, n := range clusters {
		if n >= 3 {
			repeated = true
		}
	}
	if repeated && studyKey(a) != a.LastStudyKey && s.now().Sub(a.LastStudy) >= time.Hour && ctx.Err() == nil {
		ok, e := s.learn(ctx, &a)
		if e != nil {
			r.Errors = append(r.Errors, "adapter study: "+e.Error())
		} else if ok {
			if _, e = s.publishAdaptive(ctx, &a); e != nil {
				r.Errors = append(r.Errors, e.Error())
			}
		}
	}
	if len(r.Errors) == 0 && len(r.Offers) > 0 {
		r.Status = "ok"
	}
	if r.Pages < len(ready) {
		r.Limited = true
	}
	return r
}
