package market

import (
	catalog "agent/collector"
	"testing"
)

func TestCompetitorRegistrationPersistsAndIsIdempotent(t *testing.T) {
	store := memoryStore{}
	s := New(store)
	before, e := s.configuredSources(t.Context())
	if e != nil || len(before) != 3 {
		t.Fatalf("%v %v", before, e)
	}
	if _, e = s.enableCompetitors(t.Context(), AddCompetitorsInput{Sources: []string{"allsoft", "127.0.0.1"}}); e == nil {
		t.Fatal("unapproved host accepted")
	}
	added, e := s.enableCompetitors(t.Context(), AddCompetitorsInput{Sources: []string{"https://allsoft.ru/", "www.syssoft.ru"}})
	if e != nil || len(added) != 2 {
		t.Fatalf("%v %v", added, e)
	}
	other := New(store)
	sources, e := other.configuredSources(t.Context())
	if e != nil || len(sources) != 5 {
		t.Fatalf("%v %v", sources, e)
	}
	added, e = other.enableCompetitors(t.Context(), AddCompetitorsInput{Sources: []string{"allsoft", "syssoft"}})
	if e != nil || len(added) != 0 {
		t.Fatalf("duplicate activation: %v %v", added, e)
	}
	for _, p := range catalog.CompetitorProfiles() {
		for _, u := range p.Seeds {
			if !catalog.Allowed(p, u) {
				t.Fatal("bad seed")
			}
		}
	}
}
