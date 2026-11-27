package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The HTTP client's host pacing is unchanged. Several in-flight reads let
// response latency overlap without starting requests closer together.
const scanWorkers = catalog.FetchWorkers

// Retain family pages and unresolved product bodies. Bound the store archive.
const evidenceLimit int64 = 8 << 30
const parserVersion = 3

type pageJob struct{ page, path string }
type pageOutcome struct {
	job           pageJob
	c             check
	raw           []byte
	d             catalog.Downloaded
	parseErr, err error
}

func parsePage(page string, raw []byte, at time.Time, family bool) (c check, err error) {
	c = check{Page: page, At: at, State: "observed", ParserVersion: parserVersion}
	if family {
		c.Products, err = softlinefamily.Extract(page, raw, at)
		if err == nil {
			c.Title, c.State = softlinefamily.Describe(raw, c.Products)
		}
	} else {
		c.Products, err = direct(page, raw, at)
	}
	if err != nil {
		c.State = "error"
		c.Error = err.Error()
	}
	return
}

func unresolved(c check) bool {
	if c.Error != "" || c.State == "family_without_listed_variants" {
		return true
	}
	if len(c.Products) == 0 {
		return c.State != "no_public_price" && c.State != "unavailable"
	}
	for _, p := range c.Products {
		if rank(p) < 2 {
			return true
		}
	}
	return false
}

func readEvidence(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	b, err := io.ReadAll(io.LimitReader(z, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 {
		return nil, fmt.Errorf("evidence exceeds body limit")
	}
	return b, nil
}
func bodyHash(b []byte) string                { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func evidencePath(path, digest string) string { return path + "." + digest + ".html.gz" }

func saveEvidence(path string, b []byte) error {
	return atomicFile(path, func(w io.Writer) error {
		z := gzip.NewWriter(w)
		if _, err := z.Write(b); err != nil {
			_ = z.Close()
			return err
		}
		return z.Close()
	})
}

func (s *survey) parseDownloaded(ctx context.Context, j pageJob, d catalog.Downloaded, family bool) pageOutcome {
	if d.Skip {
		return pageOutcome{job: j, d: d}
	}
	c := check{Page: j.page, At: d.At, State: "observed", ParserVersion: parserVersion}
	if d.Err == nil {
		c, pe := parsePage(j.page, d.Body, d.At, family)
		if pe == nil && !family && len(c.Products) == 1 && c.Products[0].State == "unrecognized_product" {
			if journal, _, _ := catalog.JobJournal(ctx); journal != nil {
				o, err := journal.RepairOffer(source, j.page, d.Body, d.At)
				if err != nil {
					return pageOutcome{job: j, c: c, d: d, err: err}
				}
				if o != nil {
					c.Products[0] = softlinefamily.Product{URL: o.URL, Name: o.Name, SKU: o.SKU, State: "priced", Offer: o}
				}
			}
		}
		b := d.Body
		if !family && len(c.Products) <= 1 && !unresolved(c) {
			b = nil
		}
		return pageOutcome{job: j, c: c, raw: b, d: d, parseErr: pe}
	}
	var status *catalog.HTTPStatus
	c.State = "error"
	c.Error = d.Err.Error()
	if errors.As(d.Err, &status) {
		switch status.Code {
		case 404, 410:
			c.State = "unavailable"
			c.Error = ""
			if !family {
				c.Products = []softlinefamily.Product{{URL: j.page, State: "unavailable"}}
			}
		case 403, 429, 503:
			return pageOutcome{job: j, c: c, d: d, err: d.Err}
		}
	}
	if catalog.StopRequest(d.Err) || errors.Is(d.Err, context.Canceled) {
		return pageOutcome{job: j, c: c, d: d, err: d.Err}
	}
	return pageOutcome{job: j, c: c, d: d, parseErr: d.Err}
}

func (s *survey) replayEvidence(j *catalog.Journal, c check, path string) (catalog.Evidence, error) {
	if c.BodyHash != "" {
		digest, e := hex.DecodeString(c.BodyHash)
		if e != nil || len(digest) != sha256.Size {
			return catalog.Evidence{}, fmt.Errorf("invalid retained evidence digest")
		}
		b, e := readEvidence(evidencePath(path, c.BodyHash))
		if e != nil {
			return catalog.Evidence{}, e
		}
		if bodyHash(b) != c.BodyHash {
			return catalog.Evidence{}, fmt.Errorf("retained evidence digest mismatch")
		}
		return j.Import(source.ID, c.Page, c.At, b)
	}
	ev, e := j.Latest(source.ID, c.Page)
	if errors.Is(e, sql.ErrNoRows) {
		return catalog.Evidence{}, nil
	}
	return ev, e
}

func (s *survey) scan(ctx context.Context, urls []string, family bool, get fetch) error {
	kind := "product"
	if family {
		kind = "family"
	}
	cache := filepath.Join(s.dir, kind)
	journal, _, _ := catalog.JobJournal(ctx)
	if journal != nil {
		ctx = catalog.WithJournal(ctx, journal, s.cfg.At, kind)
	}
	if s.familyDone == nil {
		s.familyDone = map[string]bool{}
	}

	if err := os.MkdirAll(cache, 0755); err != nil {
		return err
	}
	last := time.Now()
	batch := 0
	var coverageMu sync.RWMutex
	consume := func(c check, changed bool) error {
		if family && c.Error == "" && !s.familyDone[c.Page] {
			s.familyDone[c.Page] = true
			s.families++
		}
		coverageMu.Lock()
		s.merge(c)
		coverageMu.Unlock()
		if changed {
			batch++
		}
		if batch > 0 && (batch >= 50 || time.Since(last) > time.Minute) {
			if err := s.snapshot(); err != nil {
				return err
			}
			last = time.Now()
			batch = 0
		}
		return nil
	}
	var pending, localPending []pageJob
	batchApplied := 0
	var evidenceBytes int64
	if journal == nil {
		if err := filepath.WalkDir(s.out, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".html.gz") {
				info, e := d.Info()
				if e != nil {
					return e
				}
				evidenceBytes += info.Size()
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// Replay existing checkpoints first; a changed parser can reparse retained
	// bodies locally without refreshing the original observation time.
	for _, page := range urls {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !family {
			if p, ok := s.covered[page]; ok && rank(p) >= 2 {
				continue
			}
		}
		path := checkpoint(cache, page)
		var c check
		err := read(path, &c)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		valid := err == nil && c.Page == page
		if valid && journal != nil {
			if !unresolved(c) {
				if c.BodyHash != "" {
					if _, e := s.replayEvidence(journal, c, path); e != nil {
						return e
					}
				}
				if err = consume(c, false); err != nil {
					return err
				}
				continue
			}
			// Recover every old unresolved checkpoint. If its HTML was retained,
			// the next attempt parses locally. Otherwise only the first retry
			// refetches the permitted URL, then later parse retries are offline.
			ev, e := s.replayEvidence(journal, c, path)
			if e != nil {
				return e
			}
			typ, _ := catalog.FailureType(errors.New(c.Error), "parse_unclassified")
			if e = journal.SeedLegacyFailure(source.ID, page, s.cfg.At, kind, typ, c.At, ev.ID); e != nil {
				return e
			}
			if e = consume(c, false); e != nil {
				return e
			}
			if !family && s.applyVerifiedBatch(page) {
				batchApplied++
				if e = journal.Resolve(source.ID, page, s.cfg.At, kind, ev.ID); e != nil {
					return e
				}
				continue
			}
			allowed, e := journal.MayRun(source.ID, page, s.cfg.At, kind, time.Now())
			if e != nil {
				return e
			}
			if allowed {
				if ev.ID > 0 && strings.HasPrefix(typ, "parse_") {
					localPending = append(localPending, pageJob{page, path})
				} else {
					pending = append(pending, pageJob{page, path})
				}
			}
			continue
		}
		if valid && (family || unresolved(c)) && c.ParserVersion < parserVersion && c.BodyHash != "" {
			digest, e := hex.DecodeString(c.BodyHash)
			if e != nil || len(digest) != sha256.Size {
				return fmt.Errorf("invalid retained evidence digest")
			}
			raw, e := readEvidence(evidencePath(path, c.BodyHash))
			if e != nil {
				return e
			}
			if bodyHash(raw) != c.BodyHash {
				return fmt.Errorf("retained evidence digest mismatch: %s", page)
			}
			c, _ = parsePage(page, raw, c.At, family)
			c.BodyHash = bodyHash(raw)
			if e = save(path, c); e != nil {
				return e
			}
			if e = consume(c, true); e != nil {
				return e
			}
			if !family && s.applyVerifiedBatch(page) {
				batchApplied++
			}
			continue
		}
		if valid && (c.Error == "" || time.Since(c.At) < 3*time.Hour) {
			if err = consume(c, false); err != nil {
				return err
			}
			if !family && s.applyVerifiedBatch(page) {
				batchApplied++
			}
			continue
		}
		if !family && s.applyVerifiedBatch(page) {
			batchApplied++
			continue
		}
		pending = append(pending, pageJob{page, path})
	}
	if len(pending) == 0 && len(localPending) == 0 {
		return s.snapshot()
	}
	if batchApplied > 0 {
		fmt.Printf("softline verified batch filled %d previously unresolved sitemap URLs\n", batchApplied)
		if err := s.snapshot(); err != nil {
			return err
		}
	}
	if !family {
		s.prioritizePending(pending)
	}
	// One durable queue and a separate parsing pool. The existing checkpoint
	// format and writer lock remain authoritative for snapshots and exports.
	if journal != nil {
		list := make([]string, 0, len(pending)+len(localPending))
		for _, j := range localPending {
			list = append(list, j.page)
		}
		for _, j := range pending {
			list = append(list, j.page)
		}
		if e := journal.Seed(source.ID, s.cfg.At, kind, list, catalog.DemoPriority); e != nil {
			return e
		}
	}
	process := func(jobs []pageJob) error {
		var pauseErr error
		var writeErr error
		capWarned := false
		e := catalog.Pipeline(ctx, jobs, func(work context.Context, j pageJob) catalog.Downloaded {
			if !family {
				coverageMu.RLock()
				p, ok := s.covered[j.page]
				resolved := ok && rank(p) >= 2
				coverageMu.RUnlock()
				if resolved {
					s.skippedByEvidence.Add(1)
					if journal != nil {
						if err := journal.Resolve(source.ID, j.page, s.cfg.At, kind, 0); err != nil {
							return catalog.Downloaded{Err: err, At: time.Now().UTC()}
						}
					}
					return catalog.Downloaded{Skip: true}
				}
			}
			return catalog.DownloadTask(work, source, j.page, catalog.Fetch(get))
		}, func(j pageJob, d catalog.Downloaded) pageOutcome { return s.parseDownloaded(ctx, j, d, family) }, func(r pageOutcome) error {
			if r.d.Skip {
				return nil
			}
			if r.err != nil {
				var h *catalog.HTTPStatus
				if errors.As(r.err, &h) && catalog.FatalControl(r.err) {
					if e := s.recordPause(h); e != nil {
						pauseErr = e
					} else if pauseErr == nil {
						pauseErr = fmt.Errorf("source paused: %w", r.err)
					}
				}
				if e := catalog.RecordTask(ctx, source, r.job.page, r.d, false, r.parseErr, "network"); e != nil {
					return e
				}
				return r.err
			}
			if writeErr != nil {
				return writeErr
			}
			if journal == nil && len(r.raw) > 0 {
				if evidenceBytes+int64(len(r.raw)) <= evidenceLimit {
					digest := bodyHash(r.raw)
					path := evidencePath(r.job.path, digest)
					if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
						if err = saveEvidence(path, r.raw); err != nil {
							writeErr = err
							return err
						}
						if info, err := os.Stat(path); err == nil {
							evidenceBytes += info.Size()
						}
					} else if err != nil {
						writeErr = err
						return err
					}
					r.c.BodyHash = digest
				} else if !capWarned {
					fmt.Fprintln(os.Stderr, "evidence archive quota reached")
					capWarned = true
				}
			}
			if err := save(r.job.path, r.c); err != nil {
				writeErr = err
				return err
			}
			if err := consume(r.c, true); err != nil {
				writeErr = err
				return err
			}
			typ := "parse_unclassified"
			if r.c.State == "family_without_listed_variants" {
				typ = "parse_missing_family_model"
			}
			return catalog.RecordTask(ctx, source, r.job.page, r.d, !unresolved(r.c), r.parseErr, typ)
		})
		if pauseErr != nil {
			return pauseErr
		}
		return e
	}
	if len(localPending) > 0 {
		fmt.Printf("softline local-first: %d retained unresolved responses\n", len(localPending))
		if e := process(localPending); e != nil {
			_ = s.snapshot()
			return e
		}
	}
	for len(pending) > 0 {
		n := min(len(pending), 256)
		if e := process(pending[:n]); e != nil {
			_ = s.snapshot()
			return e
		}
		pending = pending[n:]
		if journal != nil {
			due, e := journal.Due(source.ID, s.cfg.At, kind, time.Now(), 128)
			if e != nil {
				return e
			}
			jobs := make([]pageJob, 0, len(due))
			for _, u := range due {
				jobs = append(jobs, pageJob{u, checkpoint(cache, u)})
			}
			if len(jobs) > 0 {
				if e = process(jobs); e != nil {
					_ = s.snapshot()
					return e
				}
			}
		}
	}
	return s.snapshot()
}

func (s *survey) drainRetries(ctx context.Context, get fetch) error {
	j, _, _ := catalog.JobJournal(ctx)
	if j == nil {
		return nil
	}
	for {
		var earliest time.Time
		for _, kind := range []string{"family", "product"} {
			due, e := j.Due(source.ID, s.cfg.At, kind, time.Now(), 256)
			if e != nil {
				return e
			}
			if len(due) > 0 {
				if e = s.scan(ctx, due, kind == "family", get); e != nil {
					return e
				}
			}
			next, ok, e := j.NextDue(source.ID, s.cfg.At, kind)
			if e != nil {
				return e
			}
			if ok && (earliest.IsZero() || next.Before(earliest)) {
				earliest = next
			}
		}
		if earliest.IsZero() {
			return nil
		}
		timer := time.NewTimer(max(time.Second, time.Until(earliest)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *survey) importLegacyArchive(j *catalog.Journal) error {
	count := 0
	for _, kind := range []string{"family", "product"} {
		paths, e := filepath.Glob(filepath.Join(s.dir, kind, "*.html.gz"))
		if e != nil {
			return e
		}
		for _, p := range paths {
			i := strings.Index(p, ".json.")
			if i < 0 {
				continue
			}
			var c check
			if e = read(p[:i+5], &c); e != nil {
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				return e
			}
			if c.BodyHash == "" || p != evidencePath(p[:i+5], c.BodyHash) {
				continue
			}
			if _, e = s.replayEvidence(j, c, p[:i+5]); e != nil {
				return e
			}
			count++
		}
	}
	fmt.Printf("softline imported %d timestamped legacy HTML responses into shared archive\n", count)
	return nil
}
