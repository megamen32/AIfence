package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"agent/internal/decision"
)

var decimalThreshold = regexp.MustCompile("^[+-]?[0-9]+([.][0-9]+)?$")

// Only the three editable fields come from the form. Source and measurement
// coordinates are copied from the caller's current private record.
func editInput(r decision.Record, values url.Values) (decision.SaveInput, error) {
	v, err := strconv.ParseInt(values.Get("version"), 10, 64)
	if err != nil || v != r.Version || r.Task != nil {
		return decision.SaveInput{}, decision.ErrVersion
	}
	title, action := strings.TrimSpace(values.Get("title")), strings.TrimSpace(values.Get("action"))
	op := values.Get("operator")
	threshold := strings.ReplaceAll(strings.TrimSpace(values.Get("threshold")), ",", ".")
	if title == "" || action == "" || (op != ">=" && op != "<=") || !decimalThreshold.MatchString(threshold) {
		return decision.SaveInput{}, errors.New("Укажите название, способ проверки и числовой порог.")
	}
	if _, err := strconv.ParseFloat(threshold, 64); err != nil {
		return decision.SaveInput{}, errors.New("Недопустимый числовой порог.")
	}
	h := r.Hypothesis
	h.Title, h.ProposedAction = title, action
	formula := "relative_change_pct"
	if h.SuccessCriterion.ControlProductKey != "" {
		formula = "difference_in_relative_changes_pp"
	}
	h.SuccessCriterion.Rule = formula + " " + op + " " + threshold
	return decision.SaveInput{Hypothesis: h, ExpectedVersion: v, EvidenceSnapshotID: r.EvidenceSnapshotID}, nil
}
func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная форма изменения гипотезы.", 400)
		return nil
	}
	id := r.PostForm.Get("id")
	if strings.TrimSpace(id) == "" {
		http.Error(w, "Укажите карточку.", 400)
		return nil
	}
	rec, err := h.svc.Get(r.Context(), decision.GetInput{HypothesisID: id})
	if err != nil {
		return err
	}
	in, err := editInput(rec, r.PostForm)
	if err == nil {
		_, err = h.svc.Save(r.Context(), in)
	}
	switch {
	case errors.Is(err, decision.ErrVersion):
		http.Error(w, "Карточка изменилась. Откройте текущую версию перед редактированием.", 409)
	case err != nil:
		http.Error(w, err.Error(), 422)
	default:
		http.Redirect(w, r, href("id", id), http.StatusSeeOther)
	}
	return nil
}
