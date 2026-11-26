package product

import (
	"testing"

	"agent/internal/evidence"
)

func TestSyntheticFocusComesFromEvidence(t *testing.T) {
	for _, tc := range []struct {
		row  evidence.Row
		want bool
	}{
		{evidence.Row{Details: `{"origin":"synthetic_fixture"}`}, true},
		{evidence.Row{Details: `{"synthetic":true}`}, true},
		{evidence.Row{Ref: evidence.SourceRef{SourceID: "synthetic/test"}}, true},
		{evidence.Row{Details: `{"origin":"imported_historical","synthetic":false}`}, false},
		{evidence.Row{Product: "demo-like-name"}, false},
	} {
		if got := syntheticFocus([]evidence.Row{tc.row}); got != tc.want {
			t.Fatalf("syntheticFocus(%+v) = %v, want %v", tc.row, got, tc.want)
		}
	}
}
