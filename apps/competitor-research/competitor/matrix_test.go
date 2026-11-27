package competitor

import (
	"testing"
)

func f64(v float64) *float64 { return &v }

func TestBuildCompetitorMatrixIgnoresUnknown(t *testing.T) {
	in := matrixInput{
		StudyID: "cr-test",
		Rows: []competitorObservation{
			{Competitor: "a.ru", Block: "search", Score: f64(5), Status: "observed"},
			{Competitor: "a.ru", Block: "price", Score: f64(1), Status: "observed"},
			{Competitor: "a.ru", Block: "purchase", Status: "unknown", Value: "Н/Д"},
			{Competitor: "a.ru", Block: "account", Status: "not_connected"},
			{Competitor: "a.ru", Block: "service", Score: f64(3), Status: "observed"},
			{Competitor: "a.ru", Block: "ux", Score: f64(4), Status: "observed"},
		},
	}
	got, err := buildCompetitorMatrix(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScoredRows != 4 || got.UnknownRows != 2 {
		t.Fatalf("rows scored=%d unknown=%d", got.ScoredRows, got.UnknownRows)
	}
	if len(got.Competitors) != 1 {
		t.Fatalf("competitors=%d", len(got.Competitors))
	}
	c := got.Competitors[0]
	if c.ScoredBlocks != 4 || c.UnknownBlocks != 2 {
		t.Fatalf("blocks scored=%d unknown=%d", c.ScoredBlocks, c.UnknownBlocks)
	}
	if c.WeightedScore == nil {
		t.Fatal("weighted score is nil")
	}
	// (5*10 + 1*25 + 3*10 + 4*10) / (10+25+10+10)
	want := 145.0 / 55.0
	if diff := *c.WeightedScore - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("weighted score=%v want=%v", *c.WeightedScore, want)
	}
	if diff := c.CoveredWeight - 55.0/100.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("covered weight=%v", c.CoveredWeight)
	}
}

func TestBuildCompetitorMatrixRequiresStudy(t *testing.T) {
	if _, err := buildCompetitorMatrix(matrixInput{}); err == nil {
		t.Fatal("expected study_id validation error")
	}
}
