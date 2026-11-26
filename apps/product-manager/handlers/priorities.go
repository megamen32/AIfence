package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/views"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func decimalValue(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}
func priorityView(i decision.PriorityItem) views.Priority {
	v := views.Priority{ID: i.HypothesisID, Title: i.Title, URL: href("id", i.HypothesisID), Level: label(evidenceLevels, i.EvidenceLevel), Version: strconv.FormatInt(i.Version, 10)}
	if p := i.Estimate; p != nil {
		v.Money = decimalValue(p.WeeklyPotentialRUB)
		v.Hours = decimalValue(p.CheckHours)
		v.Basis = p.Basis
		if p.WeeklyPotentialRUB != nil {
			v.MoneyText = rub(*p.WeeklyPotentialRUB) + " в неделю"
		}
		if p.CheckHours != nil {
			v.HoursText = strings.ReplaceAll(v.Hours, ".", ",") + " чел·ч"
		}
	}
	if i.WeeklyPotentialPerCheckHour != nil {
		v.Score = rub(*i.WeeklyPotentialPerCheckHour)
	}
	return v
}
func (h *Handler) Priorities(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	result, err := h.svc.Rank(r.Context(), evidence.Empty{})
	if err != nil {
		return err
	}
	groups := func(items []decision.PriorityItem) []views.Priority {
		out := make([]views.Priority, 0, len(items))
		for _, i := range items {
			out = append(out, priorityView(i))
		}
		return out
	}
	return views.PrioritiesPage(groups(result.Ranked), groups(result.NeedsEstimate)).Render(r.Context(), w)
}
func optionalNumber(text string) (*float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil {
		return nil, errors.New("Проверьте числовую оценку.")
	}
	return &n, nil
}
func (h *Handler) UpdateEstimate(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "private, no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Проверьте форму.", 400)
		return nil
	}
	v := r.PostForm
	version, err := strconv.ParseInt(v.Get("version"), 10, 64)
	if err != nil || version < 1 || v.Get("id") == "" {
		http.Error(w, "Откройте актуальную карточку.", 400)
		return nil
	}
	money, err := optionalNumber(v.Get("money"))
	if err != nil {
		http.Error(w, err.Error(), 422)
		return nil
	}
	hours, err := optionalNumber(v.Get("hours"))
	if err != nil {
		http.Error(w, err.Error(), 422)
		return nil
	}
	_, err = h.svc.SetEstimate(r.Context(), decision.EstimateInput{HypothesisID: v.Get("id"), ExpectedVersion: version, Estimate: decision.PlanningEstimate{WeeklyPotentialRUB: money, CheckHours: hours, Basis: v.Get("basis")}})
	switch {
	case errors.Is(err, decision.ErrVersion):
		http.Error(w, "Карточка изменилась. Откройте её ещё раз.", 409)
	case errors.Is(err, decision.ErrMissing), errors.Is(err, evidence.ErrLocalOnly):
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, err.Error(), 422)
	default:
		http.Redirect(w, r, "./priorities", http.StatusSeeOther)
	}
	return nil
}
