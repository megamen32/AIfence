package softlinefamily

import "testing"

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
