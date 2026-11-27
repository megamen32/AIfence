package main

import (
	catalog "agent/collector"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func terminalPage(s catalog.Source, p catalog.PageResult, inv inventory) bool {
	if p.At < inv.DiscoveredAt || p.EvidenceURL != "" {
		return false
	}
	if s.ID == "softmagazin" && (p.State == "priced" || p.State == "no_public_price") && p.Category != "software" {
		return false
	}
	switch p.State {
	case "priced", "no_public_price", "not_product", "unavailable", "out_of_scope", "robots_excluded", "access_denied":
		return true
	}
	return false
}

// Exact family evidence, not vendor/slug guesses, is allowed to remove a
// variant from the network queue. A family page itself still needs inspection.
func settledURLs(s catalog.Source, inv inventory, pages map[string]catalog.PageResult) map[string]bool {
	result := map[string]bool{}
	if s.ID == "softmagazin" {
		for _, r := range reconcileSoftMagazin(inv, pages) {
			switch r.State {
			case "priced", "no_public_price", "out_of_scope", "unavailable", "access_denied", "robots_excluded":
				result[r.URL] = true
			}
		}
	} else {
		for _, u := range inv.URLs {
			p := pages[u]
			if p.At < inv.DiscoveredAt || p.State == "error" {
				continue
			}
			for _, v := range p.Variants {
				if v.Status == "priced" || v.Status == "no_public_price" {
					result[v.URL] = true
				}
			}
		}
		for _, u := range inv.VariantURLs {
			if terminalPage(s, pages[u], inv) {
				result[u] = true
			}
		}
	}
	for _, u := range inv.URLs {
		result[u] = terminalPage(s, pages[u], inv)
	}
	return result
}

func seedScheduled(ctx context.Context, s catalog.Source, inv inventory, pages map[string]catalog.PageResult) error {
	j, _, _ := catalog.JobJournal(ctx)
	if j == nil {
		return fmt.Errorf("journal unavailable")
	}
	archive, e := j.ArchiveIndex(s.ID)
	if e != nil {
		return e
	}
	invAt, e := time.Parse(time.RFC3339, inv.DiscoveredAt)
	if e != nil {
		return e
	}
	settled := settledURLs(s, inv, pages)
	families := map[string]bool{}
	for _, u := range inv.URLs {
		families[u] = true
	}
	urls := unique(append(append([]string{}, inv.URLs...), inv.VariantURLs...))
	seeds := make([]catalog.TaskSeed, 0, len(urls))
	for _, u := range urls {
		p, ok := pages[u]
		prio := catalog.DemoPriority(u)
		if families[u] {
			prio += 8
		}
		lane := "product"
		if families[u] {
			lane = "family"
		}
		seed := catalog.TaskSeed{URL: u, Kind: "page", Lane: lane, Priority: prio, Done: settled[u]}
		if !seed.Done {
			ev, has := archive[u]
			if has && ev.At.Before(invAt) {
				has = false
			}
			if ok && p.At >= inv.DiscoveredAt && p.EvidenceURL == "" && (p.State == "error" || p.State == "unclassified" || p.Category == "unknown") {
				typ, _ := catalog.FailureType(errors.New(p.Error), "parse_unclassified")
				if p.Category == "unknown" && s.ID == "softmagazin" {
					typ = "parse_unknown_scope"
				}
				seed.ErrorType = typ
				seed.At, _ = time.Parse(time.RFC3339, p.At)
				// PageResult timestamps are rounded to seconds. The successful archive
				// receipt must match or follow the observation, in this generation.
				if has && !ev.At.Before(seed.At) && strings.HasPrefix(typ, "parse_") {
					seed.EvidenceID = ev.ID
				}
			} else if has {
				seed.EvidenceID = ev.ID
			}
		}
		seeds = append(seeds, seed)
	}
	if e = j.InitSchedule(); e != nil {
		return e
	}
	return j.SeedPlan(s.ID, inv.DiscoveredAt, seeds)
}

func runScheduled(ctx context.Context, s catalog.Source, dir string, inv *inventory, pages map[string]catalog.PageResult, fetch catalog.Fetch, consume func(catalog.PageResult) error) error {
	j, _, _ := catalog.JobJournal(ctx)
	if e := seedScheduled(ctx, s, *inv, pages); e != nil {
		return e
	}
	counts, e := j.ScheduleCounts(s.ID, inv.DiscoveredAt)
	if e != nil {
		return e
	}
	fmt.Printf("%s scheduler ready: %v\n", s.ID, counts)
	// Category completeness remains a separate audit. It may add family URLs,
	// but never gates the already discovered software/variant URL work.
	listingNext := time.Now()
	if s.ID == "softmagazin" {
		var a categoryAudit
		if b, e := os.ReadFile(filepath.Join(dir, "category-audit.json")); e == nil && json.Unmarshal(b, &a) == nil && a.InventoryAt == inv.DiscoveredAt {
			if a.Complete {
				listingNext = time.Now().Add(24 * time.Hour)
			}
			if t, e := time.Parse(time.RFC3339, a.NextAuditAt); e == nil && t.After(listingNext) {
				listingNext = t
			}
		}
	}
	turns := 0
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		tasks, e := j.Ready(s.ID, inv.DiscoveredAt, []string{"page"}, 32, time.Now())
		if e != nil {
			return e
		}
		if len(tasks) > 0 {
			if e = processReadyBatch(ctx, s, tasks, fetch, consume, settledURLs(s, *inv, pages)); e != nil {
				return e
			}
			turns++
		}
		if s.ID == "softmagazin" && !time.Now().Before(listingNext) && (turns >= 4 || len(tasks) == 0) {
			before := len(inv.URLs)
			e = collectListingsBudget(ctx, s, dir, inv, pages, fetch, consume, 8)
			if errors.Is(e, errListingYield) {
				listingNext = time.Now().Add(30 * time.Second)
			} else if e != nil {
				if catalog.StopRequest(e) || ctx.Err() != nil {
					return e
				}
				fmt.Fprintln(os.Stderr, "software listing audit deferred:", e)
				listingNext = time.Now().Add(3 * time.Hour)
			} else {
				listingNext = time.Now().Add(3 * time.Hour)
			}
			// Seeding is local and idempotent. Scope changes may resolve a queued
			// variant even when no additional family URL was discovered.
			if len(inv.URLs) != before || e == nil || errors.Is(e, errListingYield) {
				if err := seedScheduled(ctx, s, *inv, pages); err != nil {
					return err
				}
			}
			turns = 0
		}
		if len(tasks) > 0 {
			continue
		}
		st, e := snapshot(dir, *inv, pages)
		if e != nil {
			return e
		}
		counts, e = j.ScheduleCounts(s.ID, inv.DiscoveredAt)
		if e != nil {
			return e
		}
		if !catalog.HasWork(counts) && counts["repair"]+counts["exhausted"]+counts["blocked"] == 0 && st.State == "complete" {
			return nil
		}
		next, _, e := j.NextScheduleDue(s.ID, inv.DiscoveredAt)
		if e != nil {
			return e
		}
		if s.ID == "softmagazin" && (next.IsZero() || listingNext.Before(next)) {
			next = listingNext
		}
		// Keep an idle incomplete generation alive, so an accepted parser rule
		// is consumed on the next tick rather than waiting for a full rescan.
		if e = catalog.WaitForWork(ctx, next); e != nil {
			return e
		}
	}
}
