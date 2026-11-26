package main

import (
	"agent/collector/softlinefamily"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (r register) softlineFamilies(dir, cycle string, urls []string) error {
	for _, u := range urls {
		row := r.add("softline", "семейство", u)
		var check struct {
			Page, Title, State string
			At                 time.Time
			Products           []softlinefamily.Product
		}
		file := filepath.Join(dir, "checkpoints", cycle, "family", fmt.Sprintf("%x.json", sha256.Sum256([]byte(u))))
		err := load(file, &check)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if check.Page != u {
			return fmt.Errorf("unexpected checkpoint for %s", u)
		}
		row.State = check.State
		row.Name = check.Title
		row.At = check.At.UTC().Format(time.RFC3339)
		if row.State == "observed" { // Older generation checkpoints predate explicit family states.
			row.State = "family_without_listed_variants"
			if len(check.Products) > 0 {
				row.State = "family_with_variants"
			}
			for _, p := range check.Products {
				if p.Offer != nil {
					row.State = "family_with_prices"
					break
				}
			}
		}
	}
	return nil
}
