package market

import (
	catalog "agent/collector"
	"context"
	"fmt"
	"sort"
	"time"
)

type StudyEvidenceRow struct {
	URL             string               `json:"url"`
	Holdout         bool                 `json:"holdout"`
	Signals         catalog.StudySignals `json:"signals"`
	TrainingExcerpt string               `json:"training_excerpt,omitempty"`
}
type StudyEvidence struct {
	Store            string             `json:"store"`
	ArchivedHTML     int                `json:"archived_html"`
	TrainingEvidence int                `json:"training_price_evidence"`
	HoldoutEvidence  int                `json:"holdout_price_evidence"`
	Pages            []StudyEvidenceRow `json:"pages"`
}

func (s *Service) InspectAdaptiveStudy(ctx context.Context, in AdapterActionInput) (StudyEvidence, error) {
	a, e := s.readAdaptive(ctx, in.ID)
	if e != nil {
		return StudyEvidence{}, e
	}
	out := StudyEvidence{Store: a.ID}
	count := 0
	for _, r := range adaptiveCorpus(a) {
		sample, e := s.loadSample(ctx, a, r)
		if e != nil {
			return out, e
		}
		sig := catalog.InspectStudySignals(a.source(), r.URL, sample.Body)
		out.ArchivedHTML++
		if sig.HasQuoteEvidence() {
			if r.Holdout {
				out.HoldoutEvidence++
			} else {
				out.TrainingEvidence++
			}
		}
		if len(out.Pages) >= 30 {
			continue
		}
		row := StudyEvidenceRow{URL: r.URL, Holdout: r.Holdout, Signals: sig}
		// The model's independent evaluation pages remain out of prompt/debug
		// excerpts. Only aggregated signals for holdouts are returned.
		if !r.Holdout && count < 4 {
			text := catalog.TrainingPage(sample).HTML
			rs := []rune(text)
			if len(rs) > 4500 {
				rs = rs[:4500]
			}
			row.TrainingExcerpt = string(rs)
			count++
		}
		out.Pages = append(out.Pages, row)
	}
	return out, nil
}

type studyPage struct {
	r     AdapterReceipt
	score int
}

func (s *Service) rankedTrainingPages(ctx context.Context, a AdaptiveStore, corpus []AdapterReceipt) ([]AdapterReceipt, error) {
	var ranked []studyPage
	for _, r := range corpus {
		if r.Holdout {
			continue
		}
		sample, e := s.loadSample(ctx, a, r)
		if e != nil {
			return nil, e
		}
		score := catalog.InspectStudySignals(a.source(), r.URL, sample.Body).Score()
		if a.Tasks[r.URL].State == "repair" {
			score += 20
		}
		ranked = append(ranked, studyPage{r, score})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	var out []AdapterReceipt
	for _, p := range ranked {
		out = append(out, p.r)
	}
	return out, nil
}

// A failed onboarding must obtain new relevant evidence, rather than asking
// the model the same question repeatedly. No existing archived URL is fetched.
func (s *Service) expandInitialStudy(ctx context.Context, a *AdaptiveStore) error {
	if a.ActiveVersion != "" || a.PauseUntil.After(s.now()) {
		return nil
	}
	if a.LastDecision == nil || (a.LastDecision.TrainingPrices > 0 && a.LastDecision.HoldoutPrices > 0) {
		return nil
	}
	hints := map[string]int{}
	seen := map[string]bool{}
	train, hold := 0, 0
	for _, r := range a.Samples {
		seen[r.URL] = true
		if !r.HTML {
			continue
		}
		sample, e := s.loadSample(ctx, *a, r)
		if e != nil {
			return e
		}
		sig := catalog.InspectStudySignals(a.source(), r.URL, sample.Body)
		if sig.HasQuoteEvidence() {
			if r.Holdout {
				hold++
			} else {
				train++
			}
		}
		for u, score := range catalog.StudyLinkScores(a.source(), r.URL, sample.Body) {
			hints[u] = max(hints[u], score)
			a.enqueue(u)
		}
	}
	var urls []string
	for u, t := range a.Tasks {
		if !seen[u] && (t.State == "queued" || t.State == "retry") && !t.Next.After(s.now()) {
			urls = append(urls, u)
		}
	}
	catalog.RankStudyURLs(urls, hints)
	fetch := s.adaptiveFetcher()
	n := 0
	for _, u := range urls {
		if n >= 12 {
			break
		}
		n++
		if e := ctx.Err(); e != nil {
			return e
		}
		b, e := fetch(ctx, a.source(), u)
		if e != nil {
			a.taskFailure(u, e, s.now())
			if err := s.saveAdaptive(ctx, a); err != nil {
				return err
			}
			if a.PauseUntil.After(s.now()) {
				break
			}
			continue
		}
		sample := catalog.AdapterSample{URL: u, Body: b, CapturedAt: time.Now().UTC()}
		if e = s.captureAdaptive(ctx, a, sample, isAdapterHTML(u, b)); e != nil {
			return e
		}
		for _, link := range catalog.PublishedLinks(a.source(), u, b) {
			a.enqueue(link)
		}
		sig := catalog.InspectStudySignals(a.source(), u, b)
		last := a.Samples[len(a.Samples)-1]
		if sig.HasQuoteEvidence() {
			if last.Holdout {
				hold++
			} else {
				train++
			}
		}
		if train >= 3 && hold >= 2 && n >= 3 {
			break
		}
	}
	if n > 0 {
		a.Status = "studying_new_evidence"
		if e := s.saveAdaptive(ctx, a); e != nil {
			return e
		}
	}
	return adaptiveProgress(ctx, "study_samples", fmt.Sprintf("Added %d bounded study requests; %s", n, catalog.StudySummary(train, hold)), 0, 0)
}
