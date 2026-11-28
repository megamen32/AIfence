package main

import "testing"

func TestValidResearchView(t *testing.T) {
	for _, test := range []struct {
		input, want string
	}{
		{"matrix", "matrix"},
		{"sources", "sources"},
		{"trend", ""},
		{"", ""},
	} {
		if got := validResearchView(test.input); got != test.want {
			t.Fatalf("validResearchView(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
