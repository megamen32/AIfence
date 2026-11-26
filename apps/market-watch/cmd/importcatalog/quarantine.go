package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type offerTier struct {
	Quantity    *int64 `json:"quantity"`
	MaxQuantity *int64 `json:"max_quantity"`
}

// A file per original record keeps the audit idempotent without marking a
// rejected observation as uploaded. A corrected row has a different digest.
func quarantineReversedTier(checkpoint, source string, line int, digest string, raw []byte, tier offerTier) error {
	dir := checkpoint + ".quarantine"
	dst := filepath.Join(dir, digest+".json")
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	record := struct {
		Reason      string `json:"reason"`
		SourceFile  string `json:"source_file"`
		Line        int    `json:"line"`
		SHA256      string `json:"record_sha256"`
		Quantity    int64  `json:"quantity"`
		MaxQuantity int64  `json:"max_quantity"`
		RawJSON     string `json:"raw_json"`
	}{"max_quantity_less_than_quantity", source, line, digest, *tier.Quantity, *tier.MaxQuantity, string(raw)}
	f, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(record); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), dst); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func reversedTier(row []byte) (offerTier, bool, error) {
	var tier offerTier
	if err := json.Unmarshal(row, &tier); err != nil {
		return tier, false, fmt.Errorf("invalid collector record or quantity fields: %w", err)
	}
	return tier, tier.Quantity != nil && tier.MaxQuantity != nil &&
		*tier.Quantity > 0 && *tier.MaxQuantity > 0 && *tier.MaxQuantity < *tier.Quantity, nil
}
