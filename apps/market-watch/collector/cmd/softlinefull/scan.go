package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"compress/gzip"
	"context"
	"crypto/sha256"
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
const scanWorkers = 5

// Retain family pages and unresolved product bodies. Bound the store archive.
const evidenceLimit int64 = 8 << 30
const parserVersion = 1

type pageJob struct{ page, path string }
type pageOutcome struct {
	job pageJob
	c   check
	raw []byte
	err error
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

func (s *survey) fetchPage(ctx context.Context, j pageJob, family bool, get fetch) pageOutcome {
	b, err := get(ctx, source, j.page)
	c := check{Page: j.page, At: time.Now().UTC(), State: "observed", ParserVersion: parserVersion}
	if err == nil {
		c, _ = parsePage(j.page, b, c.At, family)
		if !family && !unresolved(c) {
			b = nil
		}
		return pageOutcome{j, c, b, nil}
	}
	var status *catalog.HTTPStatus
	c.State = "error"
	c.Error = err.Error()
	if errors.As(err, &status) {
		switch status.Code {
		case 404, 410:
			c.State = "unavailable"
			c.Error = ""
			if !family {
				c.Products = []softlinefamily.Product{{URL: j.page, State: "unavailable"}}
			}
		case 403, 429, 503:
			return pageOutcome{job: j, err: err}
		}
	}
	if ctx.Err() != nil {
		return pageOutcome{job: j, err: ctx.Err()}
	}
	return pageOutcome{j, c, nil, nil}
}

func (s *survey) scan(ctx context.Context, urls []string, family bool, get fetch) error {
	cache := filepath.Join(s.dir, "product")
	if family {
		cache = filepath.Join(s.dir, "family")
	}
	if err := os.MkdirAll(cache, 0755); err != nil {
		return err
	}
	last := time.Now()
	batch := 0
	consume := func(c check, changed bool) error {
		if family && c.Error == "" {
			s.families++
		}
		s.merge(c)
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
	var pending []pageJob
	batchApplied := 0
	var evidenceBytes int64
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
	if len(pending) == 0 {
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
	// A single consumer owns all mutable state, writes checkpoints and publishes.
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan pageJob)
	results := make(chan pageOutcome, scanWorkers)
	var wg sync.WaitGroup
	for range scanWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-work.Done():
					return
				case j, ok := <-jobs:
					if !ok {
						return
					}
					if work.Err() != nil {
						return
					}
					r := s.fetchPage(work, j, family, get)
					// Stop admitting work immediately on a fatal result; the shared HTTP
					// pacing also latches 403/429/503 before any subsequent request starts.
					if r.err != nil {
						cancel()
					}
					results <- r
					if r.err != nil {
						return
					}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, j := range pending {
			select {
			case <-work.Done():
				return
			case jobs <- j:
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	var firstErr error
	var pauseErr error
	var writeFailed bool
	capWarned := false
	for r := range results {
		if r.err != nil {
			var h *catalog.HTTPStatus
			if errors.As(r.err, &h) && (h.Code == 403 || h.Code == 429 || h.Code == 503) {
				if e := s.recordPause(h); e != nil {
					pauseErr = e
				} else if pauseErr == nil {
					pauseErr = fmt.Errorf("source paused: %w", r.err)
				}
			}
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if writeFailed {
			continue
		}
		if len(r.raw) > 0 {
			if evidenceBytes+int64(len(r.raw)) <= evidenceLimit {
				digest := bodyHash(r.raw)
				path := evidencePath(r.job.path, digest)
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					if err = saveEvidence(path, r.raw); err != nil {
						firstErr = err
						writeFailed = true
						cancel()
						continue
					}
					if info, err := os.Stat(path); err == nil {
						evidenceBytes += info.Size()
					}
				} else if err != nil {
					firstErr = err
					writeFailed = true
					cancel()
					continue
				}
				r.c.BodyHash = digest
			} else if !capWarned {
				fmt.Fprintln(os.Stderr, "evidence archive quota reached; unresolved pages remain explicitly unresolved")
				capWarned = true
			}
		}
		if err := save(r.job.path, r.c); err != nil {
			firstErr = err
			writeFailed = true
			cancel()
			continue
		}
		if err := consume(r.c, true); err != nil {
			firstErr = err
			writeFailed = true
			cancel()
		}
	}
	if err := s.snapshot(); err != nil {
		return err
	}
	if pauseErr != nil {
		return pauseErr
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
