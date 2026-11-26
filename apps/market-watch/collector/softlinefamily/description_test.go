package softlinefamily

import (
	"agent/collector"
	"testing"
)

func TestQuoteOnlyFamily(t *testing.T) {
	name, state := Describe([]byte("<body><h1>4D Server</h1>Цена на данный продукт предоставляется по запросу</body>"), nil)
	if state != "no_public_price" || name != "4D Server" {
		t.Fatal(name, state)
	}
	_, state = Describe([]byte("<body><h1>Example</h1></body>"), nil)
	if state != "family_without_listed_variants" {
		t.Fatal(state)
	}
}

func TestContradictoryQuantityCeiling(t *testing.T) {
	min, max := int64(10000), int64(9999)
	if catalog.PublicMaxQuantity(&min, &max) != nil {
		t.Fatal("contradictory HTML input bound treated as a license limit")
	}
	if catalog.PublicMaxQuantity(&max, &min) != &min {
		t.Fatal("valid upper bound lost")
	}
}
