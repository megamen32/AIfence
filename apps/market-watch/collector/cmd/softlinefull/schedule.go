package main

import (
	"agent/collector"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Restore both kinds of observations before publishing anything. The old
// family-first worker published its partial in-memory state for hours.
func (s *survey) restoreChecks() (map[string]check, map[string]check, error) {
	family := map[string]check{}
	product := map[string]check{}
	s.familyDone = map[string]bool{}
	s.families = 0
	for _, entry := range []struct {
		kind  string
		known []string
		dst   map[string]check
	}{{"family", s.cfg.Families, family}, {"product", s.cfg.Products, product}} {
		dir := filepath.Join(s.dir, entry.kind)
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, nil, e
		}
		allowed := map[string]bool{}
		for _, u := range entry.known {
			allowed[u] = true
		}
		files, e := os.ReadDir(dir)
		if e != nil {
			return nil, nil, e
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			var c check
			path := filepath.Join(dir, file.Name())
			if e = read(path, &c); e != nil {
				return nil, nil, e
			}
			if !allowed[c.Page] || checkpoint(dir, c.Page) != path {
				continue
			}
			entry.dst[c.Page] = c
			s.merge(c)
			if entry.kind == "family" && c.Error == "" && !s.familyDone[c.Page] {
				s.familyDone[c.Page] = true
				s.families++
			}
		}
	}
	return family, product, nil
}

func (s *survey) prepareSchedule(j *catalog.Journal) error {
	family, product, e := s.restoreChecks()
	if e != nil {
		return e
	}
	archive, e := j.ArchiveIndex(source.ID)
	if e != nil {
		return e
	}
	invAt, e := time.Parse(time.RFC3339, s.cfg.At)
	if e != nil {
		return e
	}
	counts, e := competitorSKUs(filepath.Dir(s.out))
	if e != nil {
		fmt.Fprintln(os.Stderr, "comparison priority unavailable:", e)
		counts = map[string]int{}
	}
	seeds := make([]catalog.TaskSeed, 0, len(s.cfg.Families)+len(s.cfg.Products))
	for _, group := range []struct {
		kind   string
		urls   []string
		checks map[string]check
	}{{"family", s.cfg.Families, family}, {"product", s.cfg.Products, product}} {
		for _, u := range group.urls {
			seed := catalog.TaskSeed{URL: u, Kind: group.kind, Priority: catalog.DemoPriority(u) + 2*counts[slugSKU(u)]}
			c, found := group.checks[u]
			if group.kind == "product" {
				if s.applyVerifiedBatch(u) {
					seed.Done = true
				}
				if p, ok := s.covered[u]; ok && rank(p) >= 2 {
					seed.Done = true
				}
			} else if found && !unresolved(c) {
				seed.Done = true
			}
			if _, ok := s.prior[u]; ok {
				seed.Priority++
			}
			if !seed.Done {
				ev, ok := archive[u]
				if ok && ev.At.Before(invAt) {
					ok = false
				}
				if found {
					seed.At = c.At
					typ, _ := catalog.FailureType(errors.New(c.Error), "parse_unclassified")
					if c.State == "family_without_listed_variants" {
						typ = "parse_missing_family_model"
					}
					if sourceVariantMissing(c) {
						typ = "source_variant_not_listed"
					}
					seed.ErrorType = typ
					// Use evidence only when it is from this checkpoint or a later response
					// that survived a crash; never give an old network retry a stale price.
					if ok && !ev.At.Before(c.At) && strings.HasPrefix(typ, "parse_") {
						seed.EvidenceID = ev.ID
					}
				} else if ok {
					seed.EvidenceID = ev.ID
				}
			}
			seeds = append(seeds, seed)
		}
	}
	if e = j.InitSchedule(); e != nil {
		return e
	}
	if e = j.SeedPlan(source.ID, s.cfg.At, seeds); e != nil {
		return e
	}
	if n, e := j.RequeueSoftlineVariantChecks(s.cfg.At); e != nil {
		return e
	} else if n > 0 {
		fmt.Printf("softline code revision queued %d archived product checks\n", n)
	}
	// Never replace the export with an incomplete family-only projection.
	return s.snapshot()
}

func (s *survey) runScheduled(ctx context.Context, get fetch) error {
	j, _, _ := catalog.JobJournal(ctx)
	if j == nil {
		return fmt.Errorf("persistent journal required")
	}
	if e := s.prepareSchedule(j); e != nil {
		return e
	}
	counts, e := j.ScheduleCounts(source.ID, s.cfg.At)
	if e != nil {
		return e
	}
	fmt.Printf("softline scheduler ready: %v, families and products interleaved\n", counts)
	var mu sync.RWMutex
	last := time.Now()
	changed := 0
	for {
		if e := ctx.Err(); e != nil {
			_ = s.snapshot()
			return e
		}
		tasks, e := j.Ready(source.ID, s.cfg.At, []string{"family", "product"}, 32, time.Now())
		if e != nil {
			return e
		}
		if len(tasks) == 0 {
			if changed > 0 {
				if e = s.snapshot(); e != nil {
					return e
				}
				changed = 0
			}
			counts, e = j.ScheduleCounts(source.ID, s.cfg.At)
			if e != nil {
				return e
			}
			if !catalog.HasWork(counts) && counts["repair"]+counts["exhausted"]+counts["blocked"] == 0 {
				return nil
			}
			next, _, e := j.NextScheduleDue(source.ID, s.cfg.At)
			if e != nil {
				return e
			}
			if e = catalog.WaitForWork(ctx, next); e != nil {
				return e
			}
			continue
		}
		type result struct {
			task catalog.ReadyTask
			out  pageOutcome
		}
		var pauseErr error
		err := catalog.Pipeline(ctx, tasks, func(work context.Context, t catalog.ReadyTask) catalog.Downloaded {
			taskCtx := catalog.WithJournal(work, j, s.cfg.At, t.Kind)
			if t.Kind == "product" {
				mu.RLock()
				p, ok := s.covered[t.URL]
				done := ok && rank(p) >= 2
				mu.RUnlock()
				if done {
					s.skippedByEvidence.Add(1)
					if e := j.Resolve(source.ID, t.URL, s.cfg.At, t.Kind, 0); e != nil {
						return catalog.Downloaded{Err: &catalog.ArchiveFailure{Err: e}, At: time.Now()}
					}
					return catalog.Downloaded{Skip: true}
				}
			}
			return catalog.DownloadReady(taskCtx, source, t, catalog.Fetch(get))
		}, func(t catalog.ReadyTask, d catalog.Downloaded) result {
			p := pageJob{t.URL, checkpoint(filepath.Join(s.dir, t.Kind), t.URL)}
			return result{t, s.parseDownloaded(catalog.WithJournal(ctx, j, s.cfg.At, t.Kind), p, d, t.Kind == "family")}
		}, func(r result) error {
			o := r.out
			if o.d.Skip {
				return nil
			}
			taskCtx := catalog.WithJournal(ctx, j, s.cfg.At, r.task.Kind)
			if o.err != nil {
				var h *catalog.HTTPStatus
				if errors.As(o.err, &h) && catalog.FatalControl(o.err) {
					if e := s.recordPause(h); e != nil {
						pauseErr = e
					} else {
						pauseErr = o.err
					}
				}
				if e := catalog.RecordTask(taskCtx, source, r.task.URL, o.d, false, o.parseErr, "network"); e != nil {
					return e
				}
				return o.err
			}
			if e := save(o.job.path, o.c); e != nil {
				return e
			}
			mu.Lock()
			if r.task.Kind == "family" && o.c.Error == "" && !s.familyDone[o.c.Page] {
				s.familyDone[o.c.Page] = true
				s.families++
			}
			s.merge(o.c)
			resolved := !unresolved(o.c)
			if r.task.Kind == "product" {
				p, ok := s.covered[r.task.URL]
				resolved = ok && rank(p) >= 2
			}
			mu.Unlock()
			typ := "parse_unclassified"
			if o.c.State == "family_without_listed_variants" {
				typ = "parse_missing_family_model"
			}
			if sourceVariantMissing(o.c) {
				typ = "source_variant_not_listed"
			}
			if e := catalog.RecordTask(taskCtx, source, r.task.URL, o.d, resolved, o.parseErr, typ); e != nil {
				return e
			}
			changed++
			if changed >= 100 || time.Since(last) >= time.Minute {
				if e := s.snapshot(); e != nil {
					return e
				}
				changed = 0
				last = time.Now()
			}
			return nil
		})
		if pauseErr != nil {
			_ = s.snapshot()
			return pauseErr
		}
		if err != nil {
			_ = s.snapshot()
			return err
		}
	}
}

func sourceVariantMissing(c check) bool {
	for _, p := range c.Products {
		if p.URL == c.Page && p.StatusEvidence == "canonical_family_does_not_list_requested_url" {
			return true
		}
	}
	return false
}
