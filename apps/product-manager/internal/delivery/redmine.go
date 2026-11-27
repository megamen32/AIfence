// Package delivery pushes owner-approved Product Manager results to the
// customer's work tools: a Redmine issue draft and a Dion message draft.
// Nothing here runs from chat or a model tool. Only the panel's explicit
// confirmation POST reaches these functions.
package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"agent/internal/decision"

	"github.com/airlockrun/agentsdk"
)

// AILabel is the visible mark on every issue the assistant drafts.
const AILabel = "сгенерирована ИИ-помощником"

// Field names created in Redmine for assistant drafts. When a Redmine
// installation has no such fields the marker goes into subject/description.
const (
	FieldAI     = "Сгенерирована ИИ-помощником"
	FieldSignal = "ID сигнала"
)

// Requester is the part of an Airlock connection handle the delivery needs.
type Requester interface {
	Request(ctx context.Context, opts agentsdk.RequestOpts) ([]byte, error)
}

// RedmineIssue is the stored receipt of one delivered issue.
type RedmineIssue struct {
	SignalID string `json:"signal_id"`
	IssueID  int    `json:"issue_id"`
	URL      string `json:"url"`
	Subject  string `json:"subject"`
	Reused   bool   `json:"reused,omitempty"`
}

// Redmine delivers approved backlog tasks to one Redmine project.
type Redmine struct {
	Conn      Requester
	DB        agentsdk.DBTX
	Project   string // Redmine project identifier
	Tracker   string // tracker name, e.g. Улучшение
	PublicURL string // browser-facing Redmine origin for links
}

// SignalKey is the idempotency key: the first producer signal of the card,
// or the hypothesis ID when the card has no signal.
func SignalKey(r decision.Record) string {
	for _, id := range r.Hypothesis.SignalIDs {
		if strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return r.Hypothesis.ID
}

// Lookup returns the stored issue for this owner and signal, if any.
func (d *Redmine) Lookup(ctx context.Context, owner, signal string) (*RedmineIssue, error) {
	var raw []byte
	err := d.DB.QueryRowContext(ctx, `SELECT receipt FROM pm_redmine_issues WHERE owner_id = $1 AND signal_id = $2`, owner, signal).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out RedmineIssue
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Deliver creates the Redmine issue for an approved card exactly once per
// owner and signal. Order: local receipt, then a search in Redmine by the
// signal field or subject marker (covers a lost local write), then create.
func (d *Redmine) Deliver(ctx context.Context, owner string, r decision.Record) (RedmineIssue, error) {
	if r.Task == nil {
		return RedmineIssue{}, errors.New("сначала подтвердите задачу на карточке")
	}
	signal := SignalKey(r)
	if signal == "" {
		return RedmineIssue{}, errors.New("у карточки нет идентификатора сигнала")
	}
	if got, err := d.Lookup(ctx, owner, signal); err != nil || got != nil {
		if got != nil {
			return *got, nil
		}
		return RedmineIssue{}, err
	}
	project, err := d.project(ctx)
	if err != nil {
		return RedmineIssue{}, err
	}
	subject := Subject(r, signal)
	issue, found, err := d.find(ctx, project, signal, subject)
	if err != nil {
		return RedmineIssue{}, err
	}
	if !found {
		issue, err = d.create(ctx, project, r, signal, subject)
		if err != nil {
			return RedmineIssue{}, err
		}
	} else {
		issue.Reused = true
	}
	raw, err := json.Marshal(issue)
	if err != nil {
		return RedmineIssue{}, err
	}
	if _, err = d.DB.ExecContext(ctx, `INSERT INTO pm_redmine_issues (owner_id, signal_id, hypothesis_id, issue_id, receipt) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (owner_id, signal_id) DO NOTHING`, owner, signal, r.Hypothesis.ID, issue.IssueID, raw); err != nil {
		return RedmineIssue{}, err
	}
	stored, err := d.Lookup(ctx, owner, signal)
	if err != nil || stored == nil {
		return issue, err
	}
	return *stored, nil
}

type projectInfo struct {
	ID        int
	TrackerID int
	FieldAI   int
	FieldSig  int
}

func (d *Redmine) project(ctx context.Context) (projectInfo, error) {
	body, err := d.Conn.Request(ctx, agentsdk.RequestOpts{Path: "/projects/" + url.PathEscape(d.Project) + ".json?include=trackers,issue_custom_fields"})
	if err != nil {
		return projectInfo{}, fmt.Errorf("Redmine: проект %s недоступен: %w", d.Project, err)
	}
	var res struct {
		Project struct {
			ID       int `json:"id"`
			Trackers []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"trackers"`
			Fields []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"issue_custom_fields"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return projectInfo{}, fmt.Errorf("Redmine: ответ проекта не JSON: %w", err)
	}
	p := projectInfo{ID: res.Project.ID}
	for _, t := range res.Project.Trackers {
		if p.TrackerID == 0 || t.Name == d.Tracker {
			p.TrackerID = t.ID
		}
		if t.Name == d.Tracker {
			break
		}
	}
	for _, f := range res.Project.Fields {
		switch f.Name {
		case FieldAI:
			p.FieldAI = f.ID
		case FieldSignal:
			p.FieldSig = f.ID
		}
	}
	if p.ID == 0 {
		return projectInfo{}, errors.New("Redmine: проект не найден")
	}
	return p, nil
}

type issueList struct {
	Issues []struct {
		ID      int    `json:"id"`
		Subject string `json:"subject"`
	} `json:"issues"`
}

func (d *Redmine) find(ctx context.Context, p projectInfo, signal, subject string) (RedmineIssue, bool, error) {
	q := url.Values{"project_id": {strconv.Itoa(p.ID)}, "status_id": {"*"}, "limit": {"5"}}
	if p.FieldSig != 0 {
		q.Set("cf_"+strconv.Itoa(p.FieldSig), signal)
	} else {
		q.Set("subject", "~"+marker(signal))
	}
	body, err := d.Conn.Request(ctx, agentsdk.RequestOpts{Path: "/issues.json?" + q.Encode()})
	if err != nil {
		return RedmineIssue{}, false, fmt.Errorf("Redmine: поиск задачи по сигналу: %w", err)
	}
	var res issueList
	if err := json.Unmarshal(body, &res); err != nil {
		return RedmineIssue{}, false, err
	}
	for _, it := range res.Issues {
		if p.FieldSig != 0 || strings.Contains(it.Subject, marker(signal)) {
			return RedmineIssue{SignalID: signal, IssueID: it.ID, URL: d.link(it.ID), Subject: it.Subject}, true, nil
		}
	}
	return RedmineIssue{}, false, nil
}

func (d *Redmine) create(ctx context.Context, p projectInfo, r decision.Record, signal, subject string) (RedmineIssue, error) {
	issue := map[string]any{
		"project_id":  p.ID,
		"subject":     subject,
		"description": Description(r, signal),
	}
	if p.TrackerID != 0 {
		issue["tracker_id"] = p.TrackerID
	}
	fields := []map[string]any{}
	if p.FieldAI != 0 {
		fields = append(fields, map[string]any{"id": p.FieldAI, "value": "1"})
	}
	if p.FieldSig != 0 {
		fields = append(fields, map[string]any{"id": p.FieldSig, "value": signal})
	}
	if len(fields) > 0 {
		issue["custom_fields"] = fields
	}
	body, err := d.Conn.Request(ctx, agentsdk.RequestOpts{Method: "POST", Path: "/issues.json", Body: map[string]any{"issue": issue}, Headers: map[string]string{"Content-Type": "application/json"}})
	if err != nil {
		return RedmineIssue{}, fmt.Errorf("Redmine: задача не создана: %w", err)
	}
	var res struct {
		Issue struct {
			ID      int    `json:"id"`
			Subject string `json:"subject"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(body, &res); err != nil || res.Issue.ID == 0 {
		return RedmineIssue{}, errors.New("Redmine: ответ без номера задачи")
	}
	return RedmineIssue{SignalID: signal, IssueID: res.Issue.ID, URL: d.link(res.Issue.ID), Subject: res.Issue.Subject}, nil
}

func (d *Redmine) link(id int) string {
	return strings.TrimRight(d.PublicURL, "/") + "/issues/" + strconv.Itoa(id)
}

func marker(signal string) string { return "[" + shortID(signal) + "]" }

func shortID(s string) string {
	if len([]rune(s)) <= 40 {
		return s
	}
	return string([]rune(s)[:40])
}

// Subject is the issue title: assistant mark, card title and signal marker.
func Subject(r decision.Record, signal string) string {
	title := strings.TrimSpace(r.Hypothesis.Title)
	if title == "" {
		title = "Проверить продуктовый сигнал"
	}
	if rs := []rune(title); len(rs) > 160 {
		title = string(rs[:160]) + "…"
	}
	return "[ИИ] " + title + " " + marker(signal)
}

// Description renders the before/after numbers and evidence IDs in Redmine
// textile. Numbers come from the verified signal contrast, never from prose.
func Description(r decision.Record, signal string) string {
	h := r.Hypothesis
	var b strings.Builder
	fmt.Fprintf(&b, "_Черновик %s. Создан после подтверждения владельцем в Product Manager._\n\n", AILabel)
	fmt.Fprintf(&b, "*Продукт:* %s\n", nonEmpty(h.ProductKey, "не указан"))
	fmt.Fprintf(&b, "*Сигнал:* @%s@\n", signal)
	if c := r.SignalContrast; c != nil {
		fmt.Fprintf(&b, "\n*Цифры до/после* (%s):\n", nonEmpty(c.Basis, "окно сигнала"))
		fmt.Fprintf(&b, "* до: %s – %s, среднее %s\n", c.Baseline.From, c.Baseline.To, num(c.BaselineMean))
		fmt.Fprintf(&b, "* после: %s – %s, среднее %s\n", c.Current.From, c.Current.To, num(c.CurrentMean))
		fmt.Fprintf(&b, "* изменение: %s%%\n", signed(c.RelativeChangePct))
		if c.ControlBaselineMean != 0 || c.ControlCurrentMean != 0 {
			fmt.Fprintf(&b, "* контроль %s: %s → %s\n", nonEmpty(h.SuccessCriterion.ControlProductKey, ""), num(c.ControlBaselineMean), num(c.ControlCurrentMean))
		}
	} else {
		b.WriteString("\n*Цифры до/после:* в снимке нет проверенного окна сигнала, смотрите доказательства.\n")
	}
	if a := strings.TrimSpace(h.ProposedAction); a != "" {
		fmt.Fprintf(&b, "\n*Что сделать:* %s\n", a)
	}
	if c := h.SuccessCriterion; c.MetricKey != "" {
		fmt.Fprintf(&b, "\n*Критерий успеха:* %s, %s, база %s – %s, наблюдать %d дн.\n", c.MetricKey, c.Rule, c.BaselineFrom, c.BaselineTo, c.ObservationDays)
	}
	ids := func(title string, list []string) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n*%s:*\n", title)
		for _, id := range list {
			fmt.Fprintf(&b, "* @%s@\n", id)
		}
	}
	ids("Evidence: сигнал", h.SignalIDs)
	ids("Evidence: подтверждения", h.SupportIDs)
	ids("Evidence: против", h.CounterIDs)
	ids("Evidence: проверка пути покупки", h.BrowserEvidenceIDs)
	if len(h.Unknowns) > 0 {
		b.WriteString("\n*Не известно:*\n")
		for _, u := range h.Unknowns {
			fmt.Fprintf(&b, "* %s\n", u)
		}
	}
	fmt.Fprintf(&b, "\nСнимок данных: @%s@. Задача PM: @%s@.\n", r.EvidenceSnapshotID, taskID(r))
	if r.CardURL != "" {
		fmt.Fprintf(&b, "Карточка: %s\n", r.CardURL)
	}
	return b.String()
}

func taskID(r decision.Record) string {
	if r.Task == nil {
		return ""
	}
	return r.Task.ID
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func num(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "нет данных"
	}
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func signed(v float64) string {
	s := num(v)
	if v > 0 {
		return "+" + s
	}
	return s
}
