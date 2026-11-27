package main

import (
	"testing"

	"agent/handlers"
)

func TestSalesReviewMode(t *testing.T) {
	requested := handlers.BriefQuery{Summary: true, Director: true}
	free, name := salesReviewMode(requested, "off")
	if name != "free_form" || free.Director || !free.Summary {
		t.Fatalf("default mode = %+v, %s", free, name)
	}
	strict, name := salesReviewMode(requested, "on")
	if name != "submit_answer_required" || !strict.Director || !strict.Summary {
		t.Fatalf("strict mode = %+v, %s", strict, name)
	}
	full := handlers.BriefQuery{Offset: 10}
	if got, _ := salesReviewMode(full, "off"); got != full {
		t.Fatalf("explicit detail request changed: %+v", got)
	}
}
