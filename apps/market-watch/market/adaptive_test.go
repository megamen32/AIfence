package market

import (
	catalog "agent/collector"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func adaptiveFixture(u, style string) []byte {
	return []byte(fmt.Sprintf(`<!doctype html><html><head><link rel="canonical" href="%s"></head><body><article class="product-%s"><h1 class="name">Published license</h1><span class="sku">SKU-%s</span><span class="price" itemprop="price">1200.50</span><span class="currency">RUB</span></article></body></html>`, u, style, strings.TrimSuffix(u[strings.LastIndex(strings.TrimSuffix(u, "/"), "/")+1:], "/")))
}
func adaptiveCase(style string) catalog.AdapterCase {
	f := func(s string) catalog.Field { return catalog.Field{Selector: s} }
	return catalog.AdapterCase{ID: style, Kind: "product", Match: ".product-" + style, Rows: ".product-" + style, Name: f(".name"), SKU: f(".sku"), Price: f(".price"), Currency: f(".currency"), Basis: "displayed_total"}
}
func TestAdaptiveStoreLearnRepairAndRollback(t *testing.T) {
	ctx := t.Context()
	store := memoryStore{}
	s := New(store)
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	root := "https://license-shop.example.com/"
	calls := 0
	pages := map[string][]byte{}
	var links strings.Builder
	links.WriteString("<html><body>")
	for i := 0; i < 6; i++ {
		u := fmt.Sprintf("%sproduct/%d/", root, i)
		pages[u] = adaptiveFixture(u, "v1")
		fmt.Fprintf(&links, `<a class="product-link" href="%s">License</a>`, u)
	}
	links.WriteString("</body></html>")
	pages[root] = []byte(links.String())
	pages[root+"robots.txt"] = []byte("User-agent: *\nAllow: /\n")
	s.adaptiveFetch = func(_ context.Context, _ catalog.Source, u string) ([]byte, error) {
		calls++
		b, ok := pages[u]
		if !ok {
			return nil, fmt.Errorf("unexpected fixture URL %s", u)
		}
		return b, nil
	}
	spec1 := catalog.AdapterSpec{Cases: []catalog.AdapterCase{adaptiveCase("v1")}, Discovery: []catalog.DiscoveryRule{{Selector: "a.product-link", PathPattern: "^/product/"}}}
	spec2 := catalog.AdapterSpec{Cases: []catalog.AdapterCase{adaptiveCase("v1"), adaptiveCase("v2")}, Discovery: spec1.Discovery}
	modelCalls := 0
	s.SetAdapterProposer(func(_ context.Context, in catalog.AdapterTraining) (catalog.AdapterSpec, error) {
		modelCalls++
		if modelCalls == 1 {
			return catalog.AdapterSpec{}, nil
		}
		if modelCalls == 2 {
			if len(in.Feedback) == 0 {
				t.Fatal("missing deterministic feedback")
			}
			return spec1, nil
		}
		if modelCalls == 3 {
			return catalog.AdapterSpec{Cases: []catalog.AdapterCase{adaptiveCase("v2")}}, nil
		}
		if modelCalls == 4 {
			if len(in.Feedback) == 0 {
				t.Fatal("regression feedback missing")
			}
			return spec2, nil
		}
		return catalog.AdapterSpec{}, fmt.Errorf("unexpected repeated model call")
	})
	added, e := s.AddAdaptiveStore(ctx, AddStoreInput{URL: root})
	if e != nil || added.Status != "active" || added.ObservedOffers != 6 || modelCalls != 2 {
		t.Fatalf("onboard: %+v %v calls=%d", added, e, modelCalls)
	}
	beforeCalls := calls
	repeat, e := s.AddAdaptiveStore(ctx, AddStoreInput{URL: root})
	if e != nil || repeat.ID != added.ID || calls != beforeCalls || modelCalls != 2 {
		t.Fatal("not idempotent", repeat, e, calls, modelCalls)
	}
	// Simulate a genuinely new markup class archived from two independent URLs.
	a, e := s.readAdaptive(ctx, added.ID)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		u := fmt.Sprintf("%sproduct/new-%d/", root, i)
		raw := adaptiveFixture(u, "v2")
		if e = s.captureAdaptive(ctx, &a, catalog.AdapterSample{URL: u, Body: raw, CapturedAt: now.Add(time.Hour)}, true); e != nil {
			t.Fatal(e)
		}
		a.Tasks[u] = AdapterTask{URL: u, State: "repair", Hash: sampleHash(raw)}
	}
	if e = s.saveAdaptive(ctx, &a); e != nil {
		t.Fatal(e)
	}
	repaired, e := s.RepairAdaptiveStore(ctx, AdapterActionInput{ID: a.ID})
	if e != nil || repaired.Status != "active" || len(repaired.Versions) != 2 || repaired.ObservedOffers != 10 || modelCalls != 4 || calls != beforeCalls {
		t.Fatalf("repair: %+v %v calls=%d HTTP=%d", repaired, e, modelCalls, calls)
	}
	if repaired.LastDecision == nil || repaired.LastDecision.Regressions != 0 || repaired.LastDecision.HoldoutImprovements == 0 {
		t.Fatal("independent repair gate not proven")
	}
	back, e := s.RollbackAdaptiveStore(ctx, AdapterRollbackInput{ID: a.ID, Version: added.ActiveVersion})
	if e != nil || back.ActiveVersion != added.ActiveVersion || len(back.Versions) != 2 {
		t.Fatal(back, e)
	}
	fresh := New(store)
	sources, e := fresh.configuredSources(ctx)
	if e != nil || len(sources) != 4 || sources[3].Adaptive == nil || catalog.AdapterVersion(*sources[3].Adaptive) != added.ActiveVersion {
		t.Fatal("active profile not persisted", e)
	}
	if _, e = s.AddAdaptiveStore(ctx, AddStoreInput{URL: "https://store.softline.ru/"}); e == nil {
		t.Fatal("existing host duplicated")
	}
}

func TestAdaptiveLearningResumesRejectedRound(t *testing.T) {
	ctx := t.Context()
	store := memoryStore{}
	s := New(store)
	root := "https://resume-shop.example.com/"
	a := AdaptiveStore{ID: adapterID("resume-shop.example.com"), Host: "resume-shop.example.com", Root: root, Status: "studying", Tasks: map[string]AdapterTask{}}
	for i := 0; i < 6; i++ {
		u := fmt.Sprintf("%sproduct/%d/", root, i)
		if e := s.captureAdaptive(ctx, &a, catalog.AdapterSample{URL: u, Body: adaptiveFixture(u, "v1"), CapturedAt: time.Now().UTC()}, true); e != nil {
			t.Fatal(e)
		}
	}
	n := 0
	s.SetAdapterProposer(func(_ context.Context, _ catalog.AdapterTraining) (catalog.AdapterSpec, error) {
		n++
		if n == 1 {
			return catalog.AdapterSpec{}, nil
		}
		return catalog.AdapterSpec{}, fmt.Errorf("interrupted model")
	})
	if ok, e := s.learn(ctx, &a); e == nil || ok {
		t.Fatal("interrupted study accepted", ok, e)
	}
	persisted, e := s.readAdaptive(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if persisted.Study == nil || persisted.Study.Round != 1 || len(persisted.Study.Feedback) == 0 {
		t.Fatalf("lost checkpoint: %+v", persisted.Study)
	}
	restarted := New(store)
	restarted.SetAdapterProposer(func(_ context.Context, in catalog.AdapterTraining) (catalog.AdapterSpec, error) {
		if len(in.Feedback) == 0 {
			t.Fatal("lost prior validation feedback")
		}
		return catalog.AdapterSpec{Cases: []catalog.AdapterCase{adaptiveCase("v1")}}, nil
	})
	if ok, e := restarted.learn(ctx, &persisted); e != nil || !ok || !persisted.ReplayPending {
		t.Fatal("resume failed", ok, e)
	}
}
