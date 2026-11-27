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
	"time"
	"unicode"

	"agent/internal/decision"
	"agent/internal/evidence"

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
	Noted       bool `json:"noted,omitempty"`
	ReviewNoted bool `json:"review_noted,omitempty"`
	JournalID   int  `json:"journal_id,omitempty"`
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
	return d.DeliverItem(ctx, owner, BacklogItem{Record: r})
}

// DeliverItem delivers an ordinary Product Manager record or a pinned
// source-watch record. A review can arrive after the issue, so a later call
// may append its comment without creating another issue.
func (d *Redmine) DeliverItem(ctx context.Context, owner string, item BacklogItem) (RedmineIssue, error) {
	if !Eligible(item.Record) {
		return RedmineIssue{}, errors.New("у карточки нет подтверждённого сигнала с измеренными периодами")
	}
	if d.Transact == nil {
		return d.deliver(ctx, owner, item)
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
		issue, err = scoped.deliver(ctx, owner, item)
		return err
	})
	return issue, err
}

// BatchResult keeps failed signals in the caller's input so a later job run
// can retry them. One Redmine failure does not stop the remaining signals.
type BatchResult struct {
	Issues []RedmineIssue
	Errors []error
}

func (d *Redmine) DeliverBatch(ctx context.Context, owner string, items []BacklogItem) BatchResult {
	result := BatchResult{}
	for _, item := range items {
		issue, err := d.DeliverItem(ctx, owner, item)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		result.Issues = append(result.Issues, issue)
	}
	return result
}

func (d *Redmine) deliver(ctx context.Context, owner string, item BacklogItem) (RedmineIssue, error) {
	r := item.Record
	signal := SignalKey(r)
	if got, err := d.Lookup(ctx, owner, signal); err != nil || got != nil {
		if got != nil {
			updated, err := d.attachReview(ctx, *got, item)
			if err != nil {
				return RedmineIssue{}, err
			}
			return updated, nil
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
		issue, found, err = d.similar(ctx, project, item, signal)
		if err != nil {
			return RedmineIssue{}, err
		}
	}
	if !found {
		issue, err = d.create(ctx, project, item, signal, Subject(r, signal))
		if err != nil {
			return RedmineIssue{}, err
		}
	} else {
		issue.Reused = true
	}
	if issue, err = d.attachReview(ctx, issue, item); err != nil {
		return RedmineIssue{}, err
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

func reviewMarker(r decision.Record, signal, review string) string {
	raw, _ := json.Marshal([]any{signal, evidenceMarker(r), strings.TrimSpace(review)})
	return "PM source-watch review " + fmt.Sprintf("%x", sha256.Sum256(raw))
}

// attachReview adds a completed source-watch review once. It re-reads the
// issue journal so a lost local receipt or a retried Redmine call cannot
// duplicate the comment.
func (d *Redmine) attachReview(ctx context.Context, issue RedmineIssue, item BacklogItem) (RedmineIssue, error) {
	review := strings.TrimSpace(item.ReviewText)
	if review == "" {
		return issue, nil
	}
	var prior named
	if err := d.call(ctx, "get_redmine_issue", map[string]any{"issue_id": issue.IssueID, "include_journals": true, "include_attachments": false}, &prior); err != nil {
		return RedmineIssue{}, err
	}
	marker := reviewMarker(item.Record, SignalKey(item.Record), review)
	if strings.Contains(prior.Description, marker) {
		issue.ReviewNoted = true
		return issue, nil
	}
	for _, journal := range prior.Journals {
		if strings.Contains(journal.Notes, marker) {
			issue.ReviewNoted, issue.JournalID = true, journal.ID
			return issue, nil
		}
	}
	var result map[string]any
	if err := d.call(ctx, "update_redmine_issue", map[string]any{"issue_id": issue.IssueID, "fields": map[string]any{"notes": ReviewNote(item.Record, SignalKey(item.Record), review)}}, &result); err != nil {
		return RedmineIssue{}, err
	}
	var confirmed named
	if err := d.call(ctx, "get_redmine_issue", map[string]any{"issue_id": issue.IssueID, "include_journals": true, "include_attachments": false}, &confirmed); err != nil {
		return RedmineIssue{}, err
	}
	for _, journal := range confirmed.Journals {
		if strings.Contains(journal.Notes, marker) {
			issue.ReviewNoted, issue.JournalID = true, journal.ID
			return issue, nil
		}
	}
	return RedmineIssue{}, errors.New("Redmine: новый разбор не найден в журнале задачи")
}

func ReviewNote(r decision.Record, signal, review string) string {
	return fmt.Sprintf("Разбор сигнала @%s@:\n%s\n%s", signal, strings.TrimSpace(review), reviewMarker(r, signal, review))
}

// A product can have several unrelated problems. Require the exact product
// and proposed intervention, or the exact issue/card title, for a match.
func relevant(it named, r decision.Record) bool {
	h := r.Hypothesis
	product, action, title := strings.TrimSpace(h.ProductKey), strings.TrimSpace(h.ProposedAction), strings.TrimSpace(h.Title)
	sameTitle := title != "" && (strings.EqualFold(strings.TrimSpace(it.Subject), title) ||
		strings.HasPrefix(it.Subject, "[ИИ] "+title+" ["))
	sameAction := action != "" && strings.Contains(it.Description, action)
	productMarker := "*Продукт:* " + product + "\n"
	technicalMarker := "Продуктовый ключ: " + product + "\n"
	return product != "" && (strings.Contains(it.Description, productMarker) || strings.Contains(it.Description, technicalMarker)) && (sameTitle || sameAction)
}

func evidenceMarker(r decision.Record) string {
	c := r.SignalContrast
	raw, _ := json.Marshal([]any{r.Hypothesis.ProductKey, r.Hypothesis.SuccessCriterion.MetricKey,
		c.Basis, c.Baseline, c.Current, c.BaselineMean, c.CurrentMean, c.RelativeChangePct})
	return "PM evidence " + fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (d *Redmine) similar(ctx context.Context, p projectInfo, item BacklogItem, signal string) (RedmineIssue, bool, error) {
	r := item.Record
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
	if err := d.call(ctx, "update_redmine_issue", map[string]any{"issue_id": it.ID, "fields": map[string]any{"notes": NoteWithReview(r, signal, item.ReviewText)}}, &res); err != nil {
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
	return NoteWithReview(r, signal, "")
}

// NoteWithReview includes an optional bounded source-watch review in the same
// idempotent note as the code-computed signal numbers.
func NoteWithReview(r decision.Record, signal, review string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Новый сигнал по этому продукту (%s): @%s@.\n", AILabel, signal)
	if c := r.SignalContrast; c != nil {
		fmt.Fprintf(&b, "Было %s %s (%s – %s), стало %s (%s – %s), изменение %s%%.\n", contrastValueLabel(c), num(c.BaselineMean), c.Baseline.From, c.Baseline.To, num(c.CurrentMean), c.Current.From, c.Current.To, signed(c.RelativeChangePct))
	}
	if a := strings.TrimSpace(r.Hypothesis.ProposedAction); a != "" {
		fmt.Fprintf(&b, "Гипотеза помощника: %s\n", a)
	}
	fmt.Fprintf(&b, "Снимок данных: @%s@. Источники: %s.\n%s", r.EvidenceSnapshotID, strings.Join(append(slices.Clone(r.Hypothesis.SignalIDs), r.Hypothesis.SupportIDs...), ", "), evidenceMarker(r))
	if review = strings.TrimSpace(review); review != "" {
		fmt.Fprintf(&b, "\n\nРазбор сигнала:\n%s\n%s", review, reviewMarker(r, signal, review))
	}
	return b.String()
}

func (d *Redmine) create(ctx context.Context, p projectInfo, item BacklogItem, signal, subject string) (RedmineIssue, error) {
	r := item.Record
	fields := map[string]any{"tracker_id": p.TrackerID, FieldAI: "1", FieldSignal: signal}
	var res struct {
		ID      int    `json:"id"`
		Subject string `json:"subject"`
		Error   any    `json:"error"`
		Issue   *named `json:"issue"`
	}
	if err := d.call(ctx, "create_redmine_issue", map[string]any{"project_id": p.ID, "subject": subject, "description": DescriptionWithReview(r, signal, item.ReviewText), "fields": fields}, &res); err != nil {
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

var displayProductNames = map[string]string{
	"astra_linux":                     "Astra Linux",
	"astra_linux_special_edition":     "Astra Linux Special Edition",
	"dr_web_desktop_security_suite":   "Dr.Web",
	"kaspersky_endpoint_security":     "Kaspersky Endpoint Security",
	"kaspersky_small_office_security": "Kaspersky Small Office Security",
	"microsoft_365_business_premium":  "Microsoft 365 Business Premium",
	"myoffice_private_cloud":          "MyOffice Private Cloud",
}

var displayMetricNames = map[string]string{
	"add_to_cart":                "добавления в корзину",
	"family_orders_idx":          "заказы",
	"orders":                     "заказы",
	"orders.relative_change_pct": "заказы",
	"orders_index":               "заказы",
	"pageviews":                  "просмотры",
	"purchase_conversion":        "конверсия в покупку",
	"revenue":                    "выручка",
	"users":                      "пользователи",
	"visits":                     "посещения",
}

// Subject is a short human-readable title. The signal field remains the
// durable idempotency key in Redmine custom fields.
func Subject(r decision.Record, signal string) string {
	product := displayProductName(r.Hypothesis.ProductKey)
	metric := displayMetric(r.Hypothesis.SuccessCriterion.MetricKey)
	change := "изменение не определено"
	week := "за выбранный период"
	if c := r.SignalContrast; c != nil {
		change = humanPercent(c.RelativeChangePct)
		week = "за неделю " + subjectWeek(c.Current)
	}
	return product + ": " + metric + " " + change + " " + week
}

// Description renders the before/after numbers and evidence IDs in Redmine
// textile. Numbers come from the verified signal contrast, never from prose.
func Description(r decision.Record, signal string) string {
	return DescriptionItem(BacklogItem{Record: r}, signal)
}

func DescriptionWithReview(r decision.Record, signal, review string) string {
	return DescriptionItem(BacklogItem{Record: r, ReviewText: review, ReviewHypothesis: reviewFirstLine(review)}, signal)
}

func DescriptionItem(item BacklogItem, signal string) string {
	r := item.Record
	h := r.Hypothesis
	var b strings.Builder
	if c := r.SignalContrast; c != nil {
		if item.Synthetic {
			b.WriteString("ДЕМО: синтетические данные\n\n")
		}
		fmt.Fprintf(&b, "%s снизились на %s за неделю %s. ", sentenceMetric(h.SuccessCriterion.MetricKey), humanPercent(c.RelativeChangePct), subjectWeek(c.Current))
	} else {
		if item.Synthetic {
			b.WriteString("ДЕМО: синтетические данные\n\n")
		}
		fmt.Fprintf(&b, "%s изменились за выбранный период. ", sentenceMetric(h.SuccessCriterion.MetricKey))
	}
	if item.ReviewHypothesis != "" {
		fmt.Fprintf(&b, "Вероятная причина по разбору помощника: %s.\n\n", item.ReviewHypothesis)
	} else {
		b.WriteString("Причина пока не установлена.\n\n")
	}
	actions := item.Actions
	if len(actions) == 0 {
		actions = defaultActions(r)
	}
	b.WriteString("Что сделать:\n")
	for _, action := range actions {
		fmt.Fprintf(&b, "- %s\n", action)
	}
	if c := r.SignalContrast; c != nil {
		weeks := (h.SuccessCriterion.ObservationDays + 6) / 7
		if weeks < 1 {
			weeks = 1
		}
		fmt.Fprintf(&b, "\nКритерий успеха: %s вернутся к уровню %s в течение %d нед.\n", sentenceMetric(h.SuccessCriterion.MetricKey), num(c.BaselineMean), weeks)
	} else {
		b.WriteString("\nКритерий успеха: показатель стабилизируется в следующем сопоставимом окне.\n")
	}
	b.WriteString("\n{{collapse(Технические детали)\n")
	fmt.Fprintf(&b, "Продуктовый ключ: %s\nСигнал: @%s@\nСнимок данных: @%s@\nЗадача PM: @%s@\n", nonEmpty(h.ProductKey, "не указан"), signal, r.EvidenceSnapshotID, taskID(r))
	if control := strings.TrimSpace(h.SuccessCriterion.ControlProductKey); control != "" {
		fmt.Fprintf(&b, "Контрольный продукт: %s\n", control)
	}
	if action := strings.TrimSpace(h.ProposedAction); action != "" {
		fmt.Fprintf(&b, "Действие: %s\n", action)
	}
	if len(item.Actions) > 0 {
		fmt.Fprintf(&b, "План действий: %s\n", strings.Join(item.Actions, " | "))
	}
	if c := r.SignalContrast; c != nil {
		fmt.Fprintf(&b, "До/после: %s → %s\nИзменение: %s%%\n", num(c.BaselineMean), num(c.CurrentMean), signed(c.RelativeChangePct))
	}
	fmt.Fprintf(&b, "Evidence: %s\n", strings.Join(append(slices.Clone(h.SignalIDs), h.SupportIDs...), ", "))
	if len(h.CounterIDs) > 0 {
		fmt.Fprintf(&b, "Evidence против: %s\n", strings.Join(h.CounterIDs, ", "))
	}
	if len(h.BrowserEvidenceIDs) > 0 {
		fmt.Fprintf(&b, "Evidence пути покупки: %s\n", strings.Join(h.BrowserEvidenceIDs, ", "))
	}
	if len(h.Unknowns) > 0 {
		fmt.Fprintf(&b, "Неизвестно: %s\n", strings.Join(h.Unknowns, "; "))
	}
	if item.ReviewText != "" {
		fmt.Fprintf(&b, "Разбор: %s\n", item.ReviewText)
	}
	if Eligible(r) {
		fmt.Fprintln(&b, evidenceMarker(r))
	}
	fmt.Fprintf(&b, "Разметка: %s\n", AILabel)
	if item.ReviewText != "" {
		fmt.Fprintf(&b, "%s\n", reviewMarker(r, signal, item.ReviewText))
	}
	b.WriteString("}}\n")
	return b.String()
}

func defaultActions(r decision.Record) []string {
	action := strings.TrimSpace(r.Hypothesis.ProposedAction)
	if action != "" && !strings.Contains(action, "источник изменения метрики") {
		return []string{action}
	}
	return []string{"Проверить путь от выбора лицензии до корзины.", "Проверить релизы и изменения за неделю сигнала."}
}

func reviewFirstLine(review string) string {
	for _, line := range strings.Split(review, "\n") {
		if strings.HasPrefix(line, "Гипотеза: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Гипотеза: "))
		}
	}
	return ""
}

func displayProductName(key string) string {
	key = strings.TrimSpace(key)
	if name, ok := displayProductNames[key]; ok {
		return name
	}
	key = strings.NewReplacer("_", " ", "-", " ").Replace(key)
	words := strings.Fields(key)
	for i, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[i] = string(runes)
	}
	return nonEmpty(strings.Join(words, " "), "Продукт")
}

func displayMetric(key string) string {
	if name, ok := displayMetricNames[key]; ok {
		return name
	}
	return displayProductName(key)
}

func sentenceMetric(key string) string {
	value := displayMetric(key)
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func humanPercent(value float64) string {
	s := strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64)
	s = strings.ReplaceAll(s, ".", ",")
	if value < 0 {
		return "−" + strings.TrimPrefix(s, "-") + "%"
	}
	if value > 0 {
		return "+" + s + "%"
	}
	return s + "%"
}

func subjectWeek(period evidence.Period) string {
	from, errFrom := time.Parse(time.DateOnly, period.From)
	to, errTo := time.Parse(time.DateOnly, period.To)
	if errFrom != nil || errTo != nil {
		return humanPeriod(period.From, period.To)
	}
	months := [...]string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
	if from.Month() == to.Month() {
		return fmt.Sprintf("%d–%d %s", from.Day(), to.Day(), months[from.Month()-1])
	}
	return fmt.Sprintf("%d %s–%d %s", from.Day(), months[from.Month()-1], to.Day(), months[to.Month()-1])
}

func contrastValueLabel(c *evidence.WindowContrast) string {
	switch c.Basis {
	case "source_watch_normalized_index":
		return "нормированный индекс (база = 100)"
	case "source_watch_family_change":
		return "наблюдаемое значение"
	default:
		return "среднее"
	}
}

func contrastValue(label string, value float64) string {
	return label + " " + num(value)
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
