package main

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestBuildCompetitorPlanDefaultsAndCriteria(t *testing.T) {
	plan, err := buildCompetitorPlan(competitorResearchInput{Company: "Store", Site: "store.softline.ru", Offer: "ПО для бизнеса"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.StudyID == "" || plan.TargetSite != "https://store.softline.ru" {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if len(plan.Criteria) != 9 || len(plan.NextActions) < 4 {
		t.Fatalf("expected complete criteria: %#v", plan)
	}
	if len(plan.Competitors) != 0 || plan.Criteria[2].Weight != 25 {
		t.Fatalf("unexpected defaults: %#v", plan)
	}
}
func TestBuildCompetitorPlanDeduplicatesAndClamps(t *testing.T) {
	plan, err := buildCompetitorPlan(competitorResearchInput{Company: "x", MaxCompetitors: 2, Competitors: []string{" a.ru ", "a.ru", "b.ru", "c.ru"}, Products: []string{" KES ", "KES", "Astra"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Competitors, ",") != "a.ru,b.ru" || strings.Join(plan.Products, ",") != "KES,Astra" {
		t.Fatalf("unexpected lists: %#v", plan)
	}
}
func TestBuildCompetitorPlanRejectsInvalidInput(t *testing.T) {
	if _, err := buildCompetitorPlan(competitorResearchInput{}); err == nil {
		t.Fatal("expected missing company error")
	}
	if _, err := buildCompetitorPlan(competitorResearchInput{Company: "x", MaxCompetitors: 21}); err == nil {
		t.Fatal("expected max competitor error")
	}
}
func TestBuildCompetitorCSVIsStableAndKeepsUnknown(t *testing.T) {
	score := 4.0
	out, err := buildCompetitorCSV(competitorCSVInput{StudyID: "cr-test", Rows: []competitorObservation{
		{Competitor: "b.ru", Block: "Цена", Criterion: "SKU", Value: "", Status: "missing", SourceURL: "https://b.ru/p"},
		{Competitor: "a.ru", Block: "Цена", Criterion: "SKU", Value: "100", Score: &score, SourceURL: "https://a.ru/p"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(out.CSV)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1][1] != "a.ru" || records[2][8] != "missing" || records[2][6] != "" {
		t.Fatalf("unexpected CSV: %#v", records)
	}
	if out.Filename != "competitor-analysis.csv" {
		t.Fatal(out.Filename)
	}
}
func TestBuildCompetitorCSVRejectsBadRows(t *testing.T) {
	if _, err := buildCompetitorCSV(competitorCSVInput{}); err == nil {
		t.Fatal("expected study id error")
	}
	if _, err := buildCompetitorCSV(competitorCSVInput{StudyID: "x", Rows: []competitorObservation{{Block: "Цена", Criterion: "x"}}}); err == nil {
		t.Fatal("expected required field error")
	}
	score := 6.0
	if _, err := buildCompetitorCSV(competitorCSVInput{StudyID: "x", Rows: []competitorObservation{{Competitor: "a", Block: "Цена", Criterion: "x", Score: &score}}}); err == nil {
		t.Fatal("expected score range error")
	}
}
