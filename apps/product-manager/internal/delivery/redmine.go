// Package delivery puts Product Manager results into the customer's work
// tools: a Redmine issue draft per verified signal and the morning Dion
// message. A background job runs it; chat and model tools cannot reach it.
package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"agent/internal/decision"

	"github.com/airlockrun/agentsdk"
)

// AILabel is the visible mark on every issue the assistant drafts.
const AILabel = "сгенерирована ИИ-помощником"

// Field names configured on the authorized Redmine project.
const (
	FieldAI     = "Сгенерирована ИИ-помощником"
	FieldSignal = "ID сигнала"
)

// Redmine delivers backlog drafts to one Redmine project through the
// Redmine MCP server. Airlock injects the assistant's API key.
type Redmine struct {
	MCP       MCPCaller
	DB        agentsdk.DBTX
	Transact  func(context.Context, *sql.TxOptions, func(agentsdk.DBTX) error) error
	Project   string // Redmine project identifier
	Tracker   string // tracker name, e.g. Улучшение
	PublicURL string // browser-facing Redmine origin for links
}

// RedmineIssue is the stored receipt of one delivered issue.
type RedmineIssue struct {
	SignalID string `json:"signal_id"`
	IssueID  int    `json:"issue_id"`
	URL      string `json:"url"`
	Subject  string `json:"subject"`
	Reused   bool   `json:"reused,omitempty"`
	// Noted marks an existing similar issue that received a comment with the
	// new signal instead of a duplicate.
	Noted     bool `json:"noted,omitempty"`
	JournalID int  `json:"journal_id,omitempty"`
}

// SignalKey is the first producer signal recorded on the card.
func SignalKey(r decision.Record) string {
	for _, id := range r.Hypothesis.SignalIDs {
		if strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// Eligible reports whether a card carries a verified signal window and may
// become a backlog draft without a human typing anything.
func Eligible(r decision.Record) bool {
	c := r.SignalContrast
	return c != nil && !r.SignalContrastUnverified && c.Basis != "" &&
		c.Baseline.From != "" && c.Baseline.To != "" && c.Current.From != "" && c.Current.To != "" &&
		finite(c.BaselineMean) && finite(c.CurrentMean) && finite(c.RelativeChangePct) &&
		r.EvidenceSnapshotID != "" && SignalKey(r) != "" && r.Hypothesis.Status != "rejected"
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

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

// Deliver creates the Redmine issue for a card exactly once per owner and
// signal. Order: local receipt, then a search in Redmine by the "ID сигнала"
// field (an issue made earlier or by hand is reused), then create.
func (d *Redmine) Deliver(ctx context.Context, owner string, r decision.Record) (RedmineIssue, error) {
	if !Eligible(r) {
		return RedmineIssue{}, errors.New("у карточки нет подтверждённого сигнала с измеренными периодами")
	}
	if d.Transact == nil {
		return d.deliver(ctx, owner, r)
	}
	var issue RedmineIssue
	err := d.Transact(ctx, nil, func(tx agentsdk.DBTX) error {
		// Serialize the remote lookup and mutation across jobs and interactive requests.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, "pm-redmine-delivery", d.Project); err != nil {
			return err
		}
		scoped := *d
		scoped.DB = tx
		var err error
		issue, err = scoped.deliver(ctx, owner, r)
		return err
	})
	return issue, err
}

func (d *Redmine) deliver(ctx context.Context, owner string, r decision.Record) (RedmineIssue, error) {
	signal := SignalKey(r)
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
	issue, found, err := d.find(ctx, project, signal)
	if err != nil {
		return RedmineIssue{}, err
	}
	if !found {
		issue, found, err = d.similar(ctx, project, r, signal)
		if err != nil {
			return RedmineIssue{}, err
		}
	}
	if !found {
		issue, err = d.create(ctx, project, r, signal, Subject(r, signal))
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
	FieldSig  int
	FieldAI   int
}

type customField struct {
	ID    int `json:"id"`
	Value any `json:"value"`
}

type named struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	Identifier   string        `json:"identifier"`
	Subject      string        `json:"subject"`
	Description  string        `json:"description"`
	Error        string        `json:"error"`
	CustomFields []customField `json:"custom_fields"`
	Journals     []struct {
		ID    int    `json:"id"`
		Notes string `json:"notes"`
	} `json:"journals"`
}

func (d *Redmine) call(ctx context.Context, tool string, args map[string]any, out any) error {
	var raw json.RawMessage
	res, err := d.MCP.CallTool(ctx, tool, args)
	if err == nil {
		err = decodeMCP(res, &raw)
	}
	if err != nil {
		return fmt.Errorf("Redmine MCP %s: %w", tool, err)
	}
	var obj struct {
		Error  any             `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Error != nil {
			return fmt.Errorf("Redmine MCP %s: %v", tool, obj.Error)
		}
		if len(obj.Result) > 0 {
			raw = obj.Result
		}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("Redmine MCP %s: %w", tool, err)
	}
	return nil
}

func (d *Redmine) list(ctx context.Context, tool string, args map[string]any) ([]named, error) {
	var items []named
	if err := d.call(ctx, tool, args, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Error != "" {
			return nil, fmt.Errorf("Redmine MCP %s: %s", tool, item.Error)
		}
	}
	return items, nil
}

func (d *Redmine) project(ctx context.Context) (projectInfo, error) {
	projects, err := d.list(ctx, "list_redmine_projects", map[string]any{})
	if err != nil {
		return projectInfo{}, err
	}
	p := projectInfo{}
	for _, it := range projects {
		if it.Identifier == d.Project || strconv.Itoa(it.ID) == d.Project {
			p.ID = it.ID
		}
	}
	if p.ID == 0 {
		return projectInfo{}, fmt.Errorf("Redmine: проект %s не найден или не доступен «ИИ помощнику»", d.Project)
	}
	trackers, err := d.list(ctx, "list_project_trackers", map[string]any{"project_id": p.ID})
	if err != nil {
		return projectInfo{}, err
	}
	for _, t := range trackers {
		if t.Name == d.Tracker {
			p.TrackerID = t.ID
		}
	}
	fields, err := d.list(ctx, "list_project_issue_custom_fields", map[string]any{"project_id": p.ID})
	if err != nil {
		return projectInfo{}, err
	}
	for _, f := range fields {
		switch f.Name {
		case FieldSignal:
			p.FieldSig = f.ID
		case FieldAI:
			p.FieldAI = f.ID
		}
	}
	if p.FieldAI == 0 || p.FieldSig == 0 || p.TrackerID == 0 {
		return projectInfo{}, errors.New("Redmine: в проекте отсутствует трекер или обязательные поля помощника")
	}
	return p, nil
}

func (d *Redmine) find(ctx context.Context, p projectInfo, signal string) (RedmineIssue, bool, error) {
	args := map[string]any{"project_id": p.ID, "status_id": "*", "limit": 100, "include_custom_fields": true,
		"filters": map[string]any{"cf_" + strconv.Itoa(p.FieldSig): signal}}
	for offset := 0; ; offset += 100 {
		args["offset"] = offset
		issues, err := d.list(ctx, "list_redmine_issues", args)
		if err != nil {
			return RedmineIssue{}, false, fmt.Errorf("Redmine: поиск задачи по сигналу: %w", err)
		}
		for _, it := range issues {
			if it.ID > 0 && slices.ContainsFunc(it.CustomFields, func(f customField) bool { return f.ID == p.FieldSig && f.Value == signal }) {
				return d.receipt(it, signal), true, nil
			}
		}
		if len(issues) < 100 {
			return RedmineIssue{}, false, nil
		}
	}
}

func (d *Redmine) receipt(it named, signal string) RedmineIssue {
	return RedmineIssue{SignalID: signal, IssueID: it.ID, URL: d.link(it.ID), Subject: it.Subject}
}

// A product can have several unrelated problems. Require the exact product
// and proposed intervention, or the exact issue/card title, for a match.
func relevant(it named, r decision.Record) bool {
	h := r.Hypothesis
	product, action, title := strings.TrimSpace(h.ProductKey), strings.TrimSpace(h.ProposedAction), strings.TrimSpace(h.Title)
	sameTitle := title != "" && (strings.EqualFold(strings.TrimSpace(it.Subject), title) ||
		strings.HasPrefix(it.Subject, "[ИИ] "+title+" ["))
	sameAction := action != "" && strings.Contains(it.Description, action)
	return product != "" && strings.Contains(it.Description, "*Продукт:* "+product+"\n") && (sameTitle || sameAction)
}

func evidenceMarker(r decision.Record) string {
	c := r.SignalContrast
	raw, _ := json.Marshal([]any{r.Hypothesis.ProductKey, r.Hypothesis.SuccessCriterion.MetricKey,
		c.Basis, c.Baseline, c.Current, c.BaselineMean, c.CurrentMean, c.RelativeChangePct})
	return "PM evidence " + fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (d *Redmine) similar(ctx context.Context, p projectInfo, r decision.Record, signal string) (RedmineIssue, bool, error) {
	var matches []named
	for offset := 0; ; offset += 100 {
		issues, err := d.list(ctx, "list_redmine_issues", map[string]any{"project_id": p.ID, "status_id": "open", "limit": 100, "offset": offset,
			"fields": []string{"id", "subject", "description"}})
		if err != nil {
			return RedmineIssue{}, false, fmt.Errorf("Redmine: сверка с бэклогом: %w", err)
		}
		for _, it := range issues {
			if it.ID > 0 && relevant(it, r) {
				matches = append(matches, it)
			}
		}
		if len(issues) < 100 {
			break
		}
	}
	if len(matches) == 0 {
		return RedmineIssue{}, false, nil
	}
	if len(matches) != 1 {
		return RedmineIssue{}, false, errors.New("Redmine: найдено несколько подходящих открытых задач")
	}
	it := matches[0]
	get := func() (named, error) {
		var issue named
		err := d.call(ctx, "get_redmine_issue", map[string]any{"issue_id": it.ID, "include_journals": true, "include_attachments": false}, &issue)
		if err == nil && issue.ID != it.ID {
			err = errors.New("Redmine: не удалось прочитать задачу и её журнал")
		}
		return issue, err
	}
	prior, err := get()
	if err != nil {
		return RedmineIssue{}, false, err
	}
	issue := d.receipt(it, signal)
	if strings.Contains(prior.Description, evidenceMarker(r)) {
		return issue, true, nil
	}
	for _, journal := range prior.Journals {
		if strings.Contains(journal.Notes, evidenceMarker(r)) || strings.Contains(journal.Notes, "@"+signal+"@") {
			issue.JournalID, issue.Noted = journal.ID, true
			return issue, true, nil
		}
	}
	var res map[string]any
	if err := d.call(ctx, "update_redmine_issue", map[string]any{"issue_id": it.ID, "fields": map[string]any{"notes": Note(r, signal)}}, &res); err != nil {
		return RedmineIssue{}, false, err
	}
	confirmed, err := get()
	if err != nil {
		return RedmineIssue{}, false, err
	}
	for _, journal := range confirmed.Journals {
		if journal.ID > 0 && strings.Contains(journal.Notes, evidenceMarker(r)) {
			issue.JournalID, issue.Noted = journal.ID, true
			return issue, true, nil
		}
	}
	return RedmineIssue{}, false, errors.New("Redmine: новый комментарий не найден в журнале задачи")
}

// Note is the comment left on an existing issue for a new signal.
func Note(r decision.Record, signal string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Новый сигнал по этому продукту (%s): @%s@.\n", AILabel, signal)
	if c := r.SignalContrast; c != nil {
		fmt.Fprintf(&b, "Было %s (%s – %s), стало %s (%s – %s), изменение %s%%.\n", num(c.BaselineMean), c.Baseline.From, c.Baseline.To, num(c.CurrentMean), c.Current.From, c.Current.To, signed(c.RelativeChangePct))
	}
	if a := strings.TrimSpace(r.Hypothesis.ProposedAction); a != "" {
		fmt.Fprintf(&b, "Гипотеза помощника: %s\n", a)
	}
	fmt.Fprintf(&b, "Снимок данных: @%s@. Источники: %s.\n%s", r.EvidenceSnapshotID, strings.Join(append(slices.Clone(r.Hypothesis.SignalIDs), r.Hypothesis.SupportIDs...), ", "), evidenceMarker(r))
	return b.String()
}

func (d *Redmine) create(ctx context.Context, p projectInfo, r decision.Record, signal, subject string) (RedmineIssue, error) {
	fields := map[string]any{"tracker_id": p.TrackerID, FieldAI: "1", FieldSignal: signal}
	var res struct {
		ID      int    `json:"id"`
		Subject string `json:"subject"`
		Error   any    `json:"error"`
		Issue   *named `json:"issue"`
	}
	if err := d.call(ctx, "create_redmine_issue", map[string]any{"project_id": p.ID, "subject": subject, "description": Description(r, signal), "fields": fields}, &res); err != nil {
		return RedmineIssue{}, err
	}
	if res.Issue != nil && res.ID == 0 {
		res.ID, res.Subject = res.Issue.ID, res.Issue.Subject
	}
	if res.Error != nil || res.ID == 0 {
		return RedmineIssue{}, fmt.Errorf("Redmine: задача не создана: %v", res.Error)
	}
	return RedmineIssue{SignalID: signal, IssueID: res.ID, URL: d.link(res.ID), Subject: res.Subject}, nil
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
	fmt.Fprintf(&b, "_Черновик %s: фоновая проверка Product Manager нашла сигнал и не нашла по нему задачи._\n\n", AILabel)
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
	if Eligible(r) {
		fmt.Fprintln(&b, evidenceMarker(r))
	}
	return b.String()
}

func taskID(r decision.Record) string {
	if r.Task == nil {
		return "нет"
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
