package market

import (
	catalog "agent/collector"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/airlockrun/agentsdk"
)

const adaptiveRoot = "adapter-state"
const adaptiveIndex = adaptiveRoot + "/index.json"
const maxAdaptiveStores = 16
const maxAdaptiveFrontier = 10000
const maxLearningRounds = 3

type AdapterProposer func(context.Context, catalog.AdapterTraining) (catalog.AdapterSpec, error)
type InvalidAdapterProposal struct{ Reason string }

func (e *InvalidAdapterProposal) Error() string { return e.Reason }

type AddStoreInput struct {
	URL string `json:"url" jsonschema:"Exact public HTTPS URL of the store, category or software product"`
}
type AdapterActionInput struct {
	ID string `json:"id"`
}
type AdapterRollbackInput struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type AdapterVersionRecord struct {
	Version      string                  `json:"version"`
	Parent       string                  `json:"parent,omitempty"`
	Spec         catalog.AdapterSpec     `json:"spec"`
	Decision     catalog.AdapterDecision `json:"decision"`
	CreatedAt    time.Time               `json:"created_at"`
	ArchiveCount int                     `json:"archive_count"`
}
type AdapterReceipt struct {
	URL     string    `json:"url"`
	At      time.Time `json:"at"`
	Hash    string    `json:"sha256"`
	Holdout bool      `json:"holdout"`
	HTML    bool      `json:"html"`
}
type AdapterTask struct {
	URL         string    `json:"url"`
	Attempts    int       `json:"attempts"`
	Next        time.Time `json:"next_attempt"`
	Error       string    `json:"error,omitempty"`
	State       string    `json:"state"`
	Hash        string    `json:"sha256,omitempty"`
	RuleVersion string    `json:"rule_version,omitempty"`
	Template    string    `json:"template,omitempty"`
}
type AdaptiveStore struct {
	ID            string                   `json:"id"`
	Host          string                   `json:"host"`
	Root          string                   `json:"root"`
	Status        string                   `json:"status"`
	ActiveVersion string                   `json:"active_version,omitempty"`
	Versions      []string                 `json:"versions"`
	LastDecision  *catalog.AdapterDecision `json:"last_decision,omitempty"`
	Samples       []AdapterReceipt         `json:"samples"`
	Tasks         map[string]AdapterTask   `json:"tasks"`
	Sitemaps      []string                 `json:"sitemaps,omitempty"`
	LastStudy     time.Time                `json:"last_study"`
	LastStudyKey  string                   `json:"last_study_key,omitempty"`
	PauseUntil    time.Time                `json:"pause_until"`
}
type AdaptiveStoreView struct {
	ID             string                   `json:"id"`
	URL            string                   `json:"url"`
	Status         string                   `json:"status"`
	ActiveVersion  string                   `json:"active_version,omitempty"`
	Versions       []string                 `json:"versions"`
	Archived       int                      `json:"archived_responses"`
	Queued         int                      `json:"queued"`
	Repair         int                      `json:"awaiting_repair"`
	Done           int                      `json:"done"`
	Blocked        int                      `json:"blocked_or_exhausted"`
	ObservedOffers int                      `json:"observed_offers,omitempty"`
	LastDecision   *catalog.AdapterDecision `json:"last_decision,omitempty"`
	PauseUntil     time.Time                `json:"pause_until"`
	Note           string                   `json:"note"`
}

func (a AdaptiveStore) view() AdaptiveStoreView {
	v := AdaptiveStoreView{ID: a.ID, URL: a.Root, Status: a.Status, ActiveVersion: a.ActiveVersion, Versions: a.Versions, Archived: len(a.Samples), LastDecision: a.LastDecision, PauseUntil: a.PauseUntil, Note: "Bounded discovery; no complete sitemap coverage claim."}
	for _, t := range a.Tasks {
		switch t.State {
		case "done":
			v.Done++
		case "repair":
			v.Repair++
		case "blocked", "exhausted":
			v.Blocked++
		default:
			v.Queued++
		}
	}
	return v
}
func (s *Service) SetAdapterProposer(p AdapterProposer) { s.adapterProposer = p }
func (s *Service) adaptiveFetcher() catalog.Fetch {
	if s.adaptiveFetch != nil {
		return s.adaptiveFetch
	}
	return catalog.NewPublicHTTP().Fetch
}
func adapterID(host string) string {
	slug := strings.NewReplacer(".", "-", "_", "-").Replace(strings.ToLower(host))
	if len(slug) > 35 {
		slug = slug[:35]
	}
	h := sha256.Sum256([]byte(host))
	return "store-" + slug + "-" + hex.EncodeToString(h[:4])
}
func validAdapterID(id string) bool {
	return regexp.MustCompile(`^store-[a-z0-9-]{1,60}$`).MatchString(id)
}
func validHash(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == sha256.Size
}
func adapterPath(id string) string { return adaptiveRoot + "/" + id }
func (s *Service) readAdaptiveIDs(ctx context.Context) ([]string, error) {
	raw, e := s.store.ReadFile(ctx, adaptiveIndex)
	if errors.Is(e, agentsdk.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var ids []string
	if e = json.Unmarshal(raw, &ids); e != nil {
		return nil, e
	}
	for _, id := range ids {
		if !validAdapterID(id) {
			return nil, fmt.Errorf("invalid persisted adapter ID")
		}
	}
	return ids, nil
}
func (s *Service) writeJSON(ctx context.Context, path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.store.WriteFile(ctx, path, bytes.NewReader(b), "application/json")
	return e
}
func (s *Service) readAdaptive(ctx context.Context, id string) (AdaptiveStore, error) {
	var a AdaptiveStore
	if !validAdapterID(id) {
		return a, fmt.Errorf("invalid adapter ID")
	}
	b, e := s.store.ReadFile(ctx, adapterPath(id)+"/state.json")
	if e != nil {
		return a, e
	}
	if e = json.Unmarshal(b, &a); e != nil {
		return a, e
	}
	if a.ID != id || adapterID(a.Host) != id || catalog.PublicStoreURL(a.Root) != nil {
		return a, fmt.Errorf("invalid persisted adapter state")
	}
	u, _ := url.Parse(a.Root)
	if u.Host != a.Host {
		return a, fmt.Errorf("adapter host mismatch")
	}
	if a.Tasks == nil {
		a.Tasks = map[string]AdapterTask{}
	}
	return a, nil
}
func (s *Service) saveAdaptive(ctx context.Context, a *AdaptiveStore) error {
	return s.writeJSON(ctx, adapterPath(a.ID)+"/state.json", a)
}
func (s *Service) version(ctx context.Context, a AdaptiveStore, id string) (AdapterVersionRecord, error) {
	var r AdapterVersionRecord
	if !validHash(id) {
		return r, fmt.Errorf("invalid version")
	}
	b, e := s.store.ReadFile(ctx, adapterPath(a.ID)+"/versions/"+id+".json")
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.Version != id || catalog.AdapterVersion(r.Spec) != id || !r.Decision.Accepted {
		return r, fmt.Errorf("version is not validated")
	}
	return r, catalog.ValidateAdapter(r.Spec)
}
func (a AdaptiveStore) source() catalog.Source { return catalog.Source{ID: a.ID, Host: a.Host} }
func (s *Service) adaptiveSources(ctx context.Context) ([]catalog.Source, error) {
	ids, e := s.readAdaptiveIDs(ctx)
	if e != nil {
		return nil, e
	}
	var out []catalog.Source
	for _, id := range ids {
		a, e := s.readAdaptive(ctx, id)
		if e != nil {
			return nil, e
		}
		if a.ActiveVersion == "" {
			continue
		}
		v, e := s.version(ctx, a, a.ActiveVersion)
		if e != nil {
			return nil, e
		}
		src := a.source()
		src.Adapter = "adaptive"
		src.Adaptive = &v.Spec
		src.Seeds = []string{a.Root}
		out = append(out, src)
	}
	return out, nil
}
func (a *AdaptiveStore) enqueue(u string) {
	if len(a.Tasks) >= maxAdaptiveFrontier || !catalog.Allowed(a.source(), u) || catalog.PublicStoreURL(u) != nil {
		return
	}
	if _, ok := a.Tasks[u]; !ok {
		a.Tasks[u] = AdapterTask{URL: u, State: "queued"}
	}
}
func (a *AdaptiveStore) taskFailure(u string, err error, now time.Time) {
	t := a.Tasks[u]
	t.URL = u
	t.Attempts++
	typ, when := catalog.FailureType(err, "network")
	t.Error = typ + ": " + err.Error()
	t.State = "retry"
	t.Next = now.Add(time.Minute * time.Duration(1<<min(t.Attempts-1, 4)))
	if when.After(t.Next) {
		t.Next = when
	}
	if t.Attempts >= 5 {
		t.State = "exhausted"
	}
	if typ == "http_404" || typ == "http_410" {
		t.State = "done"
		t.Next = now.Add(24 * time.Hour)
	}
	if typ == "robots_excluded" || typ == "forbidden" {
		t.State = "blocked"
	}
	if typ == "forbidden" || typ == "rate_limit" || typ == "http_503" {
		a.PauseUntil = maxTimeAdaptive(when, now.Add(3*time.Hour))
		t.Next = maxTimeAdaptive(t.Next, a.PauseUntil)
	}
	a.Tasks[u] = t
}
func maxTimeAdaptive(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func sampleHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Service) captureAdaptive(ctx context.Context, a *AdaptiveStore, sample catalog.AdapterSample, html bool) error {
	hash := sampleHash(sample.Body)
	for _, old := range a.Samples {
		if old.URL == sample.URL && old.Hash == hash && old.At.Equal(sample.CapturedAt) {
			return nil
		}
	}
	path := adapterPath(a.ID) + "/raw/" + hash + ".gz"
	if _, e := s.store.ReadFile(ctx, path); errors.Is(e, agentsdk.ErrNotFound) {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, e = z.Write(sample.Body); e != nil {
			return e
		}
		if e = z.Close(); e != nil {
			return e
		}
		if _, e = s.store.WriteFile(ctx, path, bytes.NewReader(b.Bytes()), "application/gzip"); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	// Holdout assignment remains fixed for a URL even after later observations.
	hold := false
	found := false
	count := 0
	for _, r := range a.Samples {
		if r.HTML {
			count++
		}
		if r.URL == sample.URL {
			hold = r.Holdout
			found = true
		}
	}
	if !found && html {
		hold = count%3 == 2
	}
	a.Samples = append(a.Samples, AdapterReceipt{sample.URL, sample.CapturedAt, hash, hold, html})
	return s.saveAdaptive(ctx, a)
}
func (s *Service) loadSample(ctx context.Context, a AdaptiveStore, r AdapterReceipt) (catalog.AdapterSample, error) {
	if !validHash(r.Hash) || !catalog.Allowed(a.source(), r.URL) {
		return catalog.AdapterSample{}, fmt.Errorf("invalid archived receipt")
	}
	b, e := s.store.ReadFile(ctx, adapterPath(a.ID)+"/raw/"+r.Hash+".gz")
	if e != nil {
		return catalog.AdapterSample{}, e
	}
	z, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return catalog.AdapterSample{}, e
	}
	defer z.Close()
	raw, e := io.ReadAll(io.LimitReader(z, (32<<20)+1))
	if e != nil {
		return catalog.AdapterSample{}, e
	}
	if len(raw) > 32<<20 || sampleHash(raw) != r.Hash {
		return catalog.AdapterSample{}, fmt.Errorf("archive digest mismatch")
	}
	return catalog.AdapterSample{URL: r.URL, Body: raw, CapturedAt: r.At, Holdout: r.Holdout}, nil
}
func isAdapterHTML(u string, b []byte) bool {
	p, e := url.Parse(u)
	if e != nil || strings.HasSuffix(p.Path, ".xml") || strings.HasSuffix(p.Path, "/robots.txt") {
		return false
	}
	v := strings.ToLower(string(b[:min(4096, len(b))]))
	return strings.Contains(v, "<html") || strings.Contains(v, "<!doctype html") || strings.Contains(v, "<body")
}
func adaptiveCorpus(a AdaptiveStore) []AdapterReceipt {
	var out []AdapterReceipt
	seen := map[string]bool{}
	for _, r := range a.Samples {
		key := r.URL + "\x00" + r.Hash
		if r.HTML && !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}
func (s *Service) learn(ctx context.Context, a *AdaptiveStore) (bool, error) {
	if s.adapterProposer == nil {
		return false, fmt.Errorf("adapter model not configured")
	}
	corpus := adaptiveCorpus(*a)
	var e error
	if len(corpus) < 3 {
		return false, fmt.Errorf("insufficient independent archived pages")
	}
	var previous *catalog.AdapterSpec
	if a.ActiveVersion != "" {
		v, e := s.version(ctx, *a, a.ActiveVersion)
		if e != nil {
			return false, e
		}
		previous = &v.Spec
	}
	training := catalog.AdapterTraining{Origin: "https://" + a.Host, Current: previous}
	// Prefer failed templates and include healthy training examples. Never
	// give the model the independent holdout bodies, even on iteration two.
	chosen := map[string]bool{}
	add := func(r AdapterReceipt) error {
		if r.Holdout || chosen[r.URL] || len(training.Pages) >= 9 {
			return nil
		}
		sample, e := s.loadSample(ctx, *a, r)
		if e != nil {
			return e
		}
		chosen[r.URL] = true
		training.Pages = append(training.Pages, catalog.TrainingPage(sample))
		return nil
	}
	for i := len(corpus) - 1; i >= 0 && len(training.Pages) < 6; i-- {
		if a.Tasks[corpus[i].URL].State == "repair" {
			if e = add(corpus[i]); e != nil {
				return false, e
			}
		}
	}
	for _, r := range corpus {
		if e = add(r); e != nil {
			return false, e
		}
		if len(training.Pages) >= 9 {
			break
		}
	}
	if len(training.Pages) == 0 {
		return false, fmt.Errorf("no training pages")
	}
	a.LastStudy = s.now().UTC()
	a.LastStudyKey = studyKey(*a)
	if e = s.saveAdaptive(ctx, a); e != nil {
		return false, e
	}
	for round := 0; round < maxLearningRounds; round++ {
		if e = ctx.Err(); e != nil {
			return false, e
		}
		spec, e := s.adapterProposer(ctx, training)
		if e != nil {
			var invalid *InvalidAdapterProposal
			if errors.As(e, &invalid) {
				training.Feedback = []string{"invalid proposal JSON: " + invalid.Reason}
				continue
			}
			a.Status = "model_unavailable"
			_ = s.saveAdaptive(ctx, a)
			return false, e
		}
		d, e := catalog.EvaluateAdapterStream(a.source(), spec, previous, func(yield func(catalog.AdapterSample) error) error {
			for _, r := range corpus {
				if e := ctx.Err(); e != nil {
					return e
				}
				sample, e := s.loadSample(ctx, *a, r)
				if e != nil {
					return e
				}
				if e = yield(sample); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return false, e
		}
		a.LastDecision = &d
		// Candidate receipts survive rejected rounds; the model does not decide.
		candidateID := catalog.AdapterVersion(spec)
		receipt := AdapterVersionRecord{candidateID, a.ActiveVersion, spec, d, s.now().UTC(), len(corpus)}
		if e = s.writeJSON(ctx, adapterPath(a.ID)+"/candidates/"+candidateID+".json", receipt); e != nil {
			return false, e
		}
		if !d.Accepted {
			// Do not leak holdout URLs/prices through feedback. Only aggregate
			// validation outcomes and errors from seen training examples.
			training.Feedback = []string{fmt.Sprintf("training priced pages=%d; held-out priced pages=%d; resolved=%d baseline=%d; regressions=%d", d.TrainingPrices, d.HoldoutPrices, d.Resolved, d.BaselineResolved, d.Regressions)}
			for _, f := range d.Feedback {
				if !strings.HasPrefix(f, "https://") {
					training.Feedback = append(training.Feedback, f)
					continue
				}
				for u := range chosen {
					if strings.HasPrefix(f, u+": ") {
						training.Feedback = append(training.Feedback, f)
						break
					}
				}
			}
			training.Current = &spec
			a.Status = "needs_review"
			if e = s.saveAdaptive(ctx, a); e != nil {
				return false, e
			}
			continue
		}
		// Version is immutable; only after writing the validated artifact do we
		// change the active pointer. Prior versions remain available for rollback.
		existing := false
		for _, id := range a.Versions {
			if id == candidateID {
				existing = true
				break
			}
		}
		if existing {
			if _, e = s.version(ctx, *a, candidateID); e != nil {
				return false, e
			}
		} else {
			if e = s.writeJSON(ctx, adapterPath(a.ID)+"/versions/"+candidateID+".json", receipt); e != nil {
				return false, e
			}
			a.Versions = append(a.Versions, candidateID)
		}
		a.ActiveVersion = candidateID
		a.Status = "active"
		if e = s.saveAdaptive(ctx, a); e != nil {
			return false, e
		}
		return true, nil
	}
	// Preserve an existing active parser and never close missing tasks on
	// a model abstention or a failed candidate.
	if a.ActiveVersion != "" {
		a.Status = "active_with_unresolved_templates"
	}
	return false, s.saveAdaptive(ctx, a)
}
func (s *Service) replayAdaptive(ctx context.Context, a *AdaptiveStore) ([]catalog.Offer, error) {
	v, e := s.version(ctx, *a, a.ActiveVersion)
	if e != nil {
		return nil, e
	}
	latest := map[string]AdapterReceipt{}
	for _, r := range a.Samples {
		if r.HTML {
			old, ok := latest[r.URL]
			if !ok || r.At.After(old.At) {
				latest[r.URL] = r
			}
		}
	}
	var offers []catalog.Offer
	for _, r := range latest {
		sample, e := s.loadSample(ctx, *a, r)
		if e != nil {
			return nil, e
		}
		result, e := catalog.ParseAdapter(a.source(), v.Spec, sample.URL, sample.Body, sample.CapturedAt)
		t := a.Tasks[r.URL]
		t.URL = r.URL
		t.Hash = r.Hash
		t.RuleVersion = v.Version
		t.Template = catalog.AdapterFingerprint(sample.URL, sample.Body)
		links := catalog.AdapterLinks(a.source(), v.Spec, sample.URL, sample.Body)
		if e != nil || (len(result.Variants) == 0 && len(links) == 0) || result.Unknown > 0 {
			t.State = "repair"
			t.Error = "unresolved template"
			if e != nil {
				t.Error = e.Error()
			}
		} else {
			t.State = "done"
			t.Next = sample.CapturedAt.Add(3 * time.Hour)
			t.Error = ""
		}
		a.Tasks[r.URL] = t
		if e == nil {
			offers = append(offers, result.Offers...)
		}
		for _, u := range links {
			a.enqueue(u)
		}
	}
	if e = s.saveAdaptive(ctx, a); e != nil {
		return nil, e
	}
	return offers, nil
}
func (s *Service) addAdaptiveLocked(ctx context.Context, in AddStoreInput) (AdaptiveStoreView, error) {
	root := strings.TrimSpace(in.URL)
	if e := catalog.PublicStoreURL(root); e != nil {
		return AdaptiveStoreView{}, e
	}
	u, _ := url.Parse(root)
	if u.Host != strings.ToLower(u.Host) {
		return AdaptiveStoreView{}, fmt.Errorf("use canonical lower-case hostname")
	}
	base, e := catalog.Sources()
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	for _, src := range append(base, catalog.CompetitorProfiles()...) {
		if strings.TrimPrefix(src.Host, "www.") == strings.TrimPrefix(u.Host, "www.") {
			return AdaptiveStoreView{}, fmt.Errorf("host already has an existing profile: %s; do not duplicate its crawler", src.ID)
		}
	}
	ids, e := s.readAdaptiveIDs(ctx)
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	id := adapterID(u.Host)
	a := AdaptiveStore{ID: id, Host: u.Host, Root: root, Status: "studying", Tasks: map[string]AdapterTask{}}
	exists := false
	for _, old := range ids {
		if old == id {
			exists = true
			break
		}
		r, e := s.readAdaptive(ctx, old)
		if e != nil {
			return AdaptiveStoreView{}, e
		}
		if strings.TrimPrefix(r.Host, "www.") == strings.TrimPrefix(u.Host, "www.") {
			return AdaptiveStoreView{}, fmt.Errorf("host already has adapter %s", old)
		}
	}
	if exists {
		a, e = s.readAdaptive(ctx, id)
		if e != nil {
			return AdaptiveStoreView{}, e
		}
		if a.ActiveVersion != "" {
			return a.view(), nil
		}
	} else {
		if len(ids) >= maxAdaptiveStores {
			return AdaptiveStoreView{}, fmt.Errorf("adaptive store limit reached")
		}
		if e = s.saveAdaptive(ctx, &a); e != nil {
			return AdaptiveStoreView{}, e
		}
		ids = append(ids, id)
		if e = s.writeJSON(ctx, adaptiveIndex, ids); e != nil {
			return AdaptiveStoreView{}, e
		}
	}
	if a.PauseUntil.After(s.now()) {
		return a.view(), nil
	}
	htmlCount := 0
	for _, r := range a.Samples {
		if r.HTML {
			htmlCount++
		}
	}
	if htmlCount < 3 {
		probe, e := catalog.ProbeStore(ctx, a.source(), a.Root, s.adaptiveFetcher(), func(c context.Context, sample catalog.AdapterSample) error {
			return s.captureAdaptive(c, &a, sample, isAdapterHTML(sample.URL, sample.Body))
		}, 10)
		for _, u := range probe.Frontier {
			a.enqueue(u)
		}
		a.Sitemaps = append(a.Sitemaps, probe.Sitemaps...)
		if e != nil {
			a.taskFailure(a.Root, e, s.now())
			a.Status = "study_blocked_or_incomplete"
			if err := s.saveAdaptive(ctx, &a); err != nil {
				return a.view(), err
			}
			v := a.view()
			v.Note = e.Error()
			return v, nil
		}
	}
	ok, e := s.learn(ctx, &a)
	if e != nil {
		a.Status = "study_incomplete"
		_ = s.saveAdaptive(ctx, &a)
		v := a.view()
		v.Note = e.Error()
		return v, nil
	}
	if !ok {
		return a.view(), nil
	}
	offers, e := s.replayAdaptive(ctx, &a)
	if e != nil {
		return a.view(), e
	}
	if s.queries != nil && len(offers) > 0 {
		if e = s.storeOffers(ctx, offers); e != nil {
			return a.view(), e
		}
	}
	out := a.view()
	out.ObservedOffers = len(offers)
	return out, nil
}
func (s *Service) AddAdaptiveStore(ctx context.Context, in AddStoreInput) (AdaptiveStoreView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addAdaptiveLocked(ctx, in)
}
func (s *Service) AdaptiveStores(ctx context.Context, _ struct{}) ([]AdaptiveStoreView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, e := s.readAdaptiveIDs(ctx)
	if e != nil {
		return nil, e
	}
	out := []AdaptiveStoreView{}
	for _, id := range ids {
		a, e := s.readAdaptive(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, a.view())
	}
	return out, nil
}
func (s *Service) RepairAdaptiveStore(ctx context.Context, in AdapterActionInput) (AdaptiveStoreView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, e := s.readAdaptive(ctx, in.ID)
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	ok, e := s.learn(ctx, &a)
	if e != nil || !ok {
		return a.view(), e
	}
	offers, e := s.replayAdaptive(ctx, &a)
	if e != nil {
		return a.view(), e
	}
	if s.queries != nil {
		if e = s.storeOffers(ctx, offers); e != nil {
			return a.view(), e
		}
	}
	v := a.view()
	v.ObservedOffers = len(offers)
	return v, nil
}
func (s *Service) RollbackAdaptiveStore(ctx context.Context, in AdapterRollbackInput) (AdaptiveStoreView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, e := s.readAdaptive(ctx, in.ID)
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	if _, e = s.version(ctx, a, in.Version); e != nil {
		return a.view(), e
	}
	found := false
	for _, v := range a.Versions {
		if v == in.Version {
			found = true
		}
	}
	if !found {
		return a.view(), fmt.Errorf("version not activated in this store")
	}
	a.ActiveVersion = in.Version
	a.Status = "active_rolled_back"
	// Rollback applies to future extraction; prior historical quotes remain
	// as dated observations, not rewritten history.
	if e = s.saveAdaptive(ctx, &a); e != nil {
		return a.view(), e
	}
	return a.view(), nil
}

func (s *Service) refreshAdaptive(ctx context.Context, src catalog.Source, at time.Time) catalog.Run {
	r := catalog.Run{Source: src.ID, Mode: "adaptive_bounded", Status: "partial", Offers: []catalog.Offer{}}
	a, e := s.readAdaptive(ctx, src.ID)
	if e != nil {
		r.Errors = []string{e.Error()}
		return r
	}
	if a.PauseUntil.After(s.now()) {
		r.Errors = []string{"source paused until " + a.PauseUntil.Format(time.RFC3339)}
		return r
	}
	// Process independent due URLs, rather than restarting discovery.
	var ready []AdapterTask
	for _, t := range a.Tasks {
		if (t.State == "queued" || t.State == "retry" || t.State == "done") && !t.Next.After(at) {
			ready = append(ready, t)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].State == "done" && ready[j].State != "done" {
			return false
		}
		if ready[i].State != "done" && ready[j].State == "done" {
			return true
		}
		if !ready[i].Next.Equal(ready[j].Next) {
			return ready[i].Next.Before(ready[j].Next)
		}
		return ready[i].URL < ready[j].URL
	})
	fetch := s.adaptiveFetcher()
	r.CandidatePages = len(ready)
	if len(ready) > 12 {
		r.Limited = true
		ready = ready[:12]
	}
	for _, t := range ready {
		if ctx.Err() != nil {
			break
		}
		r.Pages++
		body, e := fetch(ctx, src, t.URL)
		if e != nil {
			a.taskFailure(t.URL, e, s.now())
			r.Errors = append(r.Errors, t.URL+": "+e.Error())
			_ = s.saveAdaptive(ctx, &a)
			if a.PauseUntil.After(s.now()) {
				break
			}
			continue
		}
		sample := catalog.AdapterSample{URL: t.URL, Body: body, CapturedAt: s.now().UTC()}
		if e = s.captureAdaptive(ctx, &a, sample, isAdapterHTML(t.URL, body)); e != nil {
			r.Errors = append(r.Errors, e.Error())
			return r
		}
		result, e := catalog.ParseAdapter(src, *src.Adaptive, t.URL, body, sample.CapturedAt)
		t.Hash = sampleHash(body)
		t.RuleVersion = a.ActiveVersion
		t.Template = catalog.AdapterFingerprint(t.URL, body)
		t.Attempts = 0
		t.Error = ""
		links := catalog.AdapterLinks(src, *src.Adaptive, t.URL, body)
		if e != nil || (len(result.Variants) == 0 && len(links) == 0) || result.Unknown > 0 {
			t.State = "repair"
			t.Error = "unresolved template"
			if e != nil {
				t.Error = e.Error()
			}
		} else {
			t.State = "done"
			t.Next = s.now().Add(3 * time.Hour)
		}
		a.Tasks[t.URL] = t
		if e == nil {
			r.Offers = append(r.Offers, result.Offers...)
		}
		for _, u := range links {
			a.enqueue(u)
		}
		if e = s.saveAdaptive(ctx, &a); e != nil {
			r.Errors = append(r.Errors, e.Error())
			return r
		}
	}
	// A recurring template failure triggers learning, once per study interval,
	// not one model call per card. Retry uses archived HTML and bounded rounds.
	repair := 0
	clusters := map[string]int{}
	for _, t := range a.Tasks {
		if t.State == "repair" {
			repair++
			clusters[t.Template]++
		}
	}
	repeated := false
	for _, n := range clusters {
		if n >= 3 {
			repeated = true
		}
	}
	if repeated && studyKey(a) != a.LastStudyKey && s.now().Sub(a.LastStudy) >= time.Hour && ctx.Err() == nil {
		ok, e := s.learn(ctx, &a)
		if e != nil {
			r.Errors = append(r.Errors, "adapter study: "+e.Error())
		} else if ok {
			rows, e := s.replayAdaptive(ctx, &a)
			if e != nil {
				r.Errors = append(r.Errors, e.Error())
			} else {
				r.Offers = append(r.Offers, rows...)
			}
		}
	}
	if len(r.Offers) > 0 && len(r.Errors) == 0 && repair == 0 {
		r.Status = "ok"
		if r.Limited {
			r.Status = "limited"
		}
	}
	return r
}

func studyKey(a AdaptiveStore) string {
	var keys []string
	for _, t := range a.Tasks {
		if t.State == "repair" {
			keys = append(keys, t.URL+"|"+t.Hash)
		}
	}
	sort.Strings(keys)
	return sampleHash([]byte(a.ActiveVersion + "|" + strings.Join(keys, ";")))
}
