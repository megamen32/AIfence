package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// verifiedBatch is the normalization boundary for an authorized publisher export.
// It deliberately does not infer an XLSX schema or infer URLs from ambiguous SKUs.
type verifiedBatch struct {
	EvidenceURL string          `json:"evidence_url"`
	ObservedAt  time.Time       `json:"observed_at"`
	Offers      []catalog.Offer `json:"offers"`
}

func validateBatch(b verifiedBatch, known map[string]bool, since, now time.Time) error {
	if !catalog.Allowed(source, b.EvidenceURL) || b.ObservedAt.IsZero() || b.ObservedAt.Before(since) || b.ObservedAt.After(now) || len(b.Offers) == 0 {
		return fmt.Errorf("verified batch needs same-host evidence, observations in current generation, and nonempty offers")
	}
	seen := map[string]bool{}
	for _, o := range b.Offers {
		at, err := time.Parse(time.RFC3339, o.CapturedAt)
		if o.Source != source.ID || !known[o.URL] || !catalog.Allowed(source, o.URL) ||
			strings.TrimSpace(o.Name) == "" || o.PriceMinor <= 0 || o.Currency != "RUB" ||
			o.PriceBasis != "per_unit" || o.Quantity == nil || *o.Quantity <= 0 ||
			(o.MaxQuantity != nil && *o.MaxQuantity < *o.Quantity) || err != nil || !at.Equal(b.ObservedAt) || seen[o.URL] {
			return fmt.Errorf("invalid or duplicate batch observation for %q", o.URL)
		}
		seen[o.URL] = true
	}
	return nil
}

func (s *survey) loadVerifiedBatch(input string) error {
	receipt := filepath.Join(s.dir, "verified-batch.json")
	var b verifiedBatch
	var err error
	if input != "" {
		err = read(input, &b)
	} else {
		err = read(receipt, &b)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	since, err := time.Parse(time.RFC3339, s.cfg.At)
	if err != nil {
		return err
	}
	if err = validateBatch(b, s.known, since, time.Now()); err != nil {
		return err
	}
	// Persist only validated input. The generation can resume with the same
	// observation times and provenance, without reading the publisher again.
	if input != "" {
		if err = save(receipt, b); err != nil {
			return err
		}
	}
	s.verified = b
	s.batchByURL = make(map[string]catalog.Offer, len(b.Offers))
	for _, o := range b.Offers {
		s.batchByURL[o.URL] = o
	}

	return nil
}

// Fill gaps only. A directly resolved product or a family observation takes
// precedence over a supplied batch. No SKU-based joins or inferred terms.
func (s *survey) applyVerifiedBatch(page string) bool {
	if p, ok := s.covered[page]; ok && rank(p) >= 2 {
		return false
	}
	o, ok := s.batchByURL[page]
	if !ok {
		return false
	}
	s.merge(check{Page: s.verified.EvidenceURL, At: s.verified.ObservedAt, Products: []softlinefamily.Product{{URL: o.URL, Name: o.Name, SKU: o.SKU, State: "priced", Offer: &o}}})
	return true
}
