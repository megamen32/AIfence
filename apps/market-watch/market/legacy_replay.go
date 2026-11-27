package market

import (
	catalog "agent/collector"
	"agent/collector/softlinefamily"
	"agent/internal/db"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type LegacyAuditInput struct {
	Source string `json:"source"`
	Limit  int    `json:"limit,omitempty"`
}
type LegacyAuditResult struct {
	Source      string             `json:"source"`
	Snapshot    string             `json:"snapshot"`
	Pages       int                `json:"pages"`
	RawVerified int                `json:"raw_verified"`
	WithPrices  int                `json:"pages_with_prices"`
	Offers      int                `json:"price_rows"`
	States      []LegacyAuditState `json:"states"`
	Errors      int                `json:"parser_errors"`
	Example     *catalog.Offer     `json:"example,omitempty"`
}
type LegacyAuditState struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

func parseLegacyPage(src catalog.Source, kind string, u string, b []byte, at time.Time) (catalog.PageResult, error) {
	if src.ID != "softline" {
		p, e := catalog.InspectPage(src, u, bytes.NewReader(b), at)
		if src.ID == "softmagazin" && p.Category != "software" && p.Category != "hardware" {
			p.State = "unclassified"
			p.Offers = nil
		}
		return p, e
	}
	p := catalog.PageResult{URL: u, At: at.UTC().Format(time.RFC3339), State: "observed"}
	var products []softlinefamily.Product
	var e error
	if kind == "family" {
		products, e = softlinefamily.Extract(u, b, at)
		if e == nil {
			p.Title, p.State = softlinefamily.Describe(b, products)
		}
	} else {
		products, e = softlinefamily.Direct(u, b, at)
	}
	if e != nil {
		p.State = "error"
		p.Error = e.Error()
		return p, e
	}
	exact := false
	for _, v := range products {
		p.Variants = append(p.Variants, catalog.Variant{URL: v.URL, Name: v.Name, SKU: v.SKU, Status: v.State})
		if v.Offer != nil {
			p.Offers = append(p.Offers, *v.Offer)
		}
		if v.URL == u {
			exact = true
			p.StatusEvidence = v.StatusEvidence
			if kind != "family" {
				p.State = v.State
			}
		}
	}
	if kind != "family" && !exact {
		p.State = "unclassified"
	}
	return p, nil
}
func bundleMember(blob []byte, r AdapterReceipt) ([]byte, error) {
	if r.ArchiveOffset < 0 || r.ArchiveLength <= 0 || r.ArchiveOffset > int64(len(blob)) || r.ArchiveLength > int64(len(blob))-r.ArchiveOffset {
		return nil, fmt.Errorf("invalid bundle offset")
	}
	z, e := gzip.NewReader(bytes.NewReader(blob[r.ArchiveOffset : r.ArchiveOffset+r.ArchiveLength]))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	raw, e := io.ReadAll(io.LimitReader(z, (32<<20)+1))
	if e != nil || len(raw) > 32<<20 || sampleHash(raw) != r.Hash {
		return nil, fmt.Errorf("archived body checksum mismatch")
	}
	return raw, nil
}

// Read-only native parser canary. It reads the migrated files via the SDK,
// checks every raw hash and uses the same Go parsing logic as the live
// specialized collectors. It neither requests storefronts nor writes offers.
func (s *Service) AuditLegacyArchive(ctx context.Context, in LegacyAuditInput) (LegacyAuditResult, error) {
	out := LegacyAuditResult{Source: in.Source}
	if s.conn == nil {
		return out, fmt.Errorf("native PostgreSQL required")
	}
	src, e := legacySource(in.Source)
	if e != nil {
		return out, e
	}
	id := adapterID(src.Host)
	meta, e := s.queries.AdaptiveStoreMeta(ctx, id)
	if e != nil {
		return out, e
	}
	var a AdaptiveStore
	if e = json.Unmarshal(meta.Metadata, &a); e != nil {
		return out, e
	}
	if a.LegacySource != src.ID || a.Status != "migration_staged" {
		return out, fmt.Errorf("audit requires a staged source")
	}
	out.Snapshot = a.MigrationSnapshot
	limit := in.Limit
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("limit must be 1..100")
	}
	rows, e := s.queries.LegacyAuditResponses(ctx, db.LegacyAuditResponsesParams{StoreID: id, MaxRows: int32(limit)})
	if e != nil {
		return out, e
	}
	type row struct {
		r    AdapterReceipt
		kind string
	}
	var items []row
	for _, v := range rows {
		var r AdapterReceipt
		if e = json.Unmarshal(v.Payload, &r); e != nil {
			return out, e
		}
		items = append(items, row{r, v.Kind})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].r.ArchivePath < items[j].r.ArchivePath })
	var path string
	var blob []byte
	states := map[string]int{}
	for _, v := range items {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		r := v.r
		prefix := adapterPath(id) + "/legacy/"
		base := strings.TrimSuffix(strings.TrimPrefix(r.ArchivePath, prefix), ".tar")
		if !strings.HasPrefix(r.ArchivePath, prefix) || !strings.HasSuffix(r.ArchivePath, ".tar") || !validHash(base) {
			return out, fmt.Errorf("invalid native bundle path")
		}
		if path != r.ArchivePath {
			blob, e = s.store.ReadFile(ctx, r.ArchivePath)
			if e != nil {
				return out, e
			}
			if sampleHash(blob) != base {
				return out, fmt.Errorf("native bundle checksum mismatch")
			}
			path = r.ArchivePath
		}
		raw, e := bundleMember(blob, r)
		if e != nil {
			return out, e
		}
		out.RawVerified++
		p, e := parseLegacyPage(src, v.kind, r.URL, raw, r.At)
		out.Pages++
		if e != nil {
			out.Errors++
		}
		states[p.State]++
		if len(p.Offers) > 0 {
			out.WithPrices++
			out.Offers += len(p.Offers)
			if out.Example == nil {
				x := p.Offers[0]
				out.Example = &x
			}
		}
	}
	for state, n := range states {
		out.States = append(out.States, LegacyAuditState{state, n})
	}
	sort.Slice(out.States, func(i, j int) bool { return out.States[i].State < out.States[j].State })
	return out, nil
}
