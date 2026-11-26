package main

import "testing"

func TestUnknownAndDeniedCannotCloseCoverage(t *testing.T) {
	c := counts()
	for _, state := range []string{"priced", "no_public_price", "unavailable", "out_of_scope", "not_product", "unclassified", "access_denied", "new_unknown_state", "pending", "family_without_listed_variants"} {
		c.add(state)
	}
	if c.Total != 10 || len(c.Unresolved) != 5 {
		t.Fatal(c)
	}
}
