package main

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agent/internal/decision"
	"agent/internal/delivery"
	"agent/internal/evidence"
	"agent/internal/product"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

// customerDelivery is the background backlog loop the customer asked for at
// kick-off: the agent itself files described tasks into Redmine with the
// "сгенерирована ИИ-помощником" flag after checking the backlog for an
// existing task, and sends a timely morning note to the product owner in Dion.
// The owner starts the loop once by opening the panel; jobs then re-queue
// themselves. Chat and model tools cannot reach these writes.
type customerDelivery struct {
	p       product.Service
	db      *agentsdk.AgentDB
	redmine *delivery.Redmine
	dion    *delivery.Dion
	sync    *agentsdk.JobHandle[slotInput, slotOutput]
	morning *agentsdk.JobHandle[slotInput, slotOutput]
	project *agentsdk.EnvVarHandle
	public  *agentsdk.EnvVarHandle
}

type slotInput struct {
	At string `json:"at"`
}
type slotOutput struct {
	Status  string   `json:"status"`
	Issues  []int    `json:"issues,omitempty"`
	DraftID string   `json:"draft_id,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

const (
	redmineMCP = "http://127.0.0.1:18460/mcp"
	userioMCP  = "https://msg.bezrabotnyi.com/mcp"
	// 04:00 UTC is 09:00 at the owner's UTC+5 office.
	morningHourUTC = 4
	syncEvery      = time.Hour
)

func registerCustomerDelivery(a *agentsdk.Agent, p product.Service) *customerDelivery {
	// Redmine MCP (scripts/stand/redmine-mcp) in legacy-per-user mode: Airlock
	// injects the assistant's API key as X-Redmine-API-Key on every call.
	rm := a.RegisterMCP(&agentsdk.MCP{Slug: "redmine", Name: "Redmine", URL: redmineMCP, AuthMode: agentsdk.MCPAuthToken,
		AuthInjection: agentsdk.AuthInjection{Type: agentsdk.AuthInjectAPIKey, Name: "X-Redmine-API-Key"}, BindingMode: agentsdk.BindingShared})
	io := a.RegisterMCP(&agentsdk.MCP{Slug: "userio", Name: "UserIO · Dion", URL: userioMCP, AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingPerUser})
	project := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "redmine_project", Description: "Идентификатор проекта Redmine для задач помощника.", Default: "personal", Pattern: `^[a-z0-9][a-z0-9_-]{0,99}$`})
	public := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "redmine_public_url", Description: "Адрес Redmine для ссылок в панели.", Default: "http://redmine.localhost:42080", Pattern: `^https?://[^\s/]+(/[^\s]*)?$`})
	chat := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "dion_test_chat", Description: "ID тестового чата Dion в UserIO (conv_…). Сводка уходит только в него; пусто = Dion выключен.", Default: "", Pattern: `^(|conv_[A-Za-z0-9]{8,64})$`})
	d := &customerDelivery{p: p, db: a.DB(), project: project, public: public}
	d.redmine = &delivery.Redmine{MCP: rm, DB: a.DB(), Transact: a.DB().Transaction, Tracker: "Улучшение"}
	d.dion = &delivery.Dion{MCP: io, DB: a.DB(), Chat: chat.Get}
	d.sync = agentsdk.RegisterJob(a, &agentsdk.Job[slotInput, slotOutput]{
		Name: "sync_backlog", Version: 1,
		Description: "Compare the owner's verified signals with the Redmine backlog: reuse or comment on an existing task, otherwise file a new one flagged as AI-generated.",
		Timeout:     3 * time.Minute, MaxAttempts: 3, MaxConcurrency: 1, Handler: d.runSync,
	})
	d.morning = agentsdk.RegisterJob(a, &agentsdk.Job[slotInput, slotOutput]{
		Name: "morning_dion", Version: 1,
		Description: "Send the owner's morning top-deviation note to the configured Dion test chat.",
		Timeout:     2 * time.Minute, MaxAttempts: 3, MaxConcurrency: 1, Handler: d.runMorning,
	})
	for _, r := range []*agentsdk.Route{
		{Method: "GET", Path: "/delivery", Handler: d.page, Description: "Бэклог Redmine и сводка в Dion"},
		{Method: "POST", Path: "/delivery/sync", Handler: sameOrigin(d.syncNow), Description: "Сверить сигналы с бэклогом сейчас"},
		{Method: "POST", Path: "/delivery/dion", Handler: sameOrigin(d.dionNow), Description: "Отправить сводку в тестовый чат Dion сейчас"},
	} {
		r.Access = agentsdk.AccessUser
		r.Handler = evidence.LocalHTTP(r.Handler)
		a.RegisterRoute(r)
	}
	return d
}

func (d *customerDelivery) configure(ctx context.Context) {
	if v, err := d.project.Get(ctx); err == nil && v != "" {
		d.redmine.Project = v
	}
	if v, err := d.public.Get(ctx); err == nil && v != "" {
		d.redmine.PublicURL = v
	}
}

func caller(ctx context.Context) (string, error) {
	c := agentsdk.CallerFromContext(ctx)
	u, ok := c.User()
	if !ok || u.ID == "" || (c.Access() != agentsdk.AccessUser && c.Access() != agentsdk.AccessAdmin) {
		return "", errors.New("нужен авторизованный владелец")
	}
	return u.ID, nil
}

// Ensure keeps both background jobs queued for the owner. It is called on
// every panel view; a stored next slot makes repeated calls cheap.
func (d *customerDelivery) Ensure(ctx context.Context) {
	owner, err := caller(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	d.queue(ctx, owner, "sync", d.sync, now.Add(5*time.Second), now)
	d.queue(ctx, owner, "morning", d.morning, nextMorning(now), now)
}

func (d *customerDelivery) queue(ctx context.Context, owner, kind string, job *agentsdk.JobHandle[slotInput, slotOutput], at, now time.Time) {
	var next time.Time
	err := d.db.QueryRowContext(ctx, `SELECT next_at FROM pm_delivery_schedule WHERE owner_id = $1 AND kind = $2`, owner, kind).Scan(&next)
	if err == nil && next.After(now.Add(-10*time.Minute)) {
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	d.enqueue(ctx, owner, kind, job, at)
}

func (d *customerDelivery) enqueue(ctx context.Context, owner, kind string, job *agentsdk.JobHandle[slotInput, slotOutput], at time.Time) {
	at = at.UTC().Truncate(time.Second)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("aifence/"+kind+"/"+owner+"/"+at.Format(time.RFC3339))).String()
	if _, err := job.EnqueueAt(ctx, id, at, slotInput{At: at.Format(time.RFC3339)}); err != nil {
		if _, lookup := job.Get(ctx, id); lookup != nil {
			slog.WarnContext(ctx, "Product Manager: background job not queued", "kind", kind, "error", err.Error())
			return
		}
	}
	_, _ = d.db.ExecContext(ctx, `INSERT INTO pm_delivery_schedule (owner_id, kind, next_at) VALUES ($1,$2,$3) ON CONFLICT (owner_id, kind) DO UPDATE SET next_at = EXCLUDED.next_at`, owner, kind, at)
}

func nextMorning(now time.Time) time.Time {
	t := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), morningHourUTC, 0, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

func (d *customerDelivery) records(ctx context.Context) ([]decision.Record, error) {
	list, err := d.p.List(ctx, evidence.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]decision.Record, 0, len(list.Records))
	for _, r := range list.Records {
		full, err := d.p.Get(ctx, decision.GetInput{HypothesisID: r.Hypothesis.ID})
		if err != nil {
			continue
		}
		out = append(out, full)
	}
	return out, nil
}

// syncBacklog files or reuses one Redmine issue per verified signal.
func (d *customerDelivery) syncBacklog(ctx context.Context, owner string) slotOutput {
	d.configure(ctx)
	out := slotOutput{Status: "ok"}
	recs, err := d.records(ctx)
	if err != nil {
		return slotOutput{Status: "failed", Errors: []string{err.Error()}}
	}
	for _, r := range recs {
		if !delivery.Eligible(r) {
			continue
		}
		issue, err := d.redmine.Deliver(ctx, owner, r)
		if err != nil {
			out.Status, out.Errors = "partial", append(out.Errors, err.Error())
			continue
		}
		out.Issues = append(out.Issues, issue.IssueID)
	}
	return out
}

func (d *customerDelivery) runSync(ctx context.Context, _ agentsdk.JobContext, _ slotInput) (slotOutput, error) {
	owner, err := caller(ctx)
	if err != nil {
		return slotOutput{}, err
	}
	out := d.syncBacklog(ctx, owner)
	d.enqueue(ctx, owner, "sync", d.sync, time.Now().Add(syncEvery))
	return out, nil
}

func (d *customerDelivery) sendMorning(ctx context.Context, owner string) slotOutput {
	recs, err := d.records(ctx)
	if err != nil {
		return slotOutput{Status: "failed", Errors: []string{err.Error()}}
	}
	now := time.Now()
	dr, err := d.dion.Prepare(ctx, owner, now, recs)
	if err == nil && !dr.Sent {
		dr, err = d.dion.Send(ctx, owner, dr.Day, now)
	}
	if err != nil {
		return slotOutput{Status: "failed", DraftID: dr.DraftID, Errors: []string{err.Error()}}
	}
	return slotOutput{Status: "sent", DraftID: dr.DraftID}
}

func (d *customerDelivery) runMorning(ctx context.Context, _ agentsdk.JobContext, _ slotInput) (slotOutput, error) {
	owner, err := caller(ctx)
	if err != nil {
		return slotOutput{}, err
	}
	// Backlog first, so the morning note links fresh issues.
	d.syncBacklog(ctx, owner)
	out := d.sendMorning(ctx, owner)
	d.enqueue(ctx, owner, "morning", d.morning, nextMorning(time.Now().Add(time.Minute)))
	return out, nil
}

// AfterApprove files the issue at once when the owner confirms a card by hand.
func (d *customerDelivery) AfterApprove(ctx context.Context, r decision.Record) {
	owner, err := caller(ctx)
	if err != nil {
		return
	}
	d.Ensure(ctx)
	d.configure(ctx)
	if _, err := d.redmine.Deliver(ctx, owner, r); err != nil {
		slog.WarnContext(ctx, "Product Manager: Redmine delivery failed", "hypothesis", r.Hypothesis.ID, "error", err.Error())
	}
}

// TaskLink shows the Redmine issue instead of the local task ID.
func (d *customerDelivery) TaskLink(ctx context.Context, r decision.Record) (string, string, bool) {
	owner, err := caller(ctx)
	if err != nil {
		return "", "", false
	}
	got, err := d.redmine.Lookup(ctx, owner, delivery.SignalKey(r))
	if err != nil || got == nil {
		return "", "", false
	}
	return "Redmine #" + strconv.Itoa(got.IssueID), got.URL, true
}

func sameOrigin(next agentsdk.RouteHandlerFunc) agentsdk.RouteHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if origin := r.Header.Get("Origin"); origin != "" {
			// Airlock replaces X-Forwarded-Host with the public request host
			// before forwarding to this authenticated application runtime.
			host := r.Host
			if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
				host = forwarded
			}
			u, err := url.Parse(origin)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
				u.User != nil || u.Path != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" ||
				!strings.EqualFold(u.Host, host) {
				http.Error(w, "Запустите действие из панели Product Manager.", http.StatusForbidden)
				return nil
			}
		}
		return next(w, r)
	}
}

func (d *customerDelivery) syncNow(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	out := d.syncBacklog(r.Context(), owner)
	return d.render(w, r, strings.Join(out.Errors, "; "))
}

func (d *customerDelivery) dionNow(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	out := d.sendMorning(r.Context(), owner)
	return d.render(w, r, strings.Join(out.Errors, "; "))
}

type deliveryView struct {
	Error    string
	Issues   []delivery.RedmineIssue
	Waiting  []decision.Record
	Draft    *delivery.DionDraft
	ChatSet  bool
	NextSync string
	NextDion string
}

func (d *customerDelivery) page(w http.ResponseWriter, r *http.Request) error {
	d.Ensure(r.Context())
	return d.render(w, r, "")
}

func (d *customerDelivery) next(ctx context.Context, owner, kind string) string {
	var t time.Time
	if err := d.db.QueryRowContext(ctx, `SELECT next_at FROM pm_delivery_schedule WHERE owner_id = $1 AND kind = $2`, owner, kind).Scan(&t); err != nil {
		return "не запланировано"
	}
	return t.UTC().Add(5 * time.Hour).Format("02.01 15:04")
}

func (d *customerDelivery) render(w http.ResponseWriter, r *http.Request, msg string) error {
	ctx := r.Context()
	owner, err := caller(ctx)
	if err != nil {
		return err
	}
	d.configure(ctx)
	v := deliveryView{Error: msg, NextSync: d.next(ctx, owner, "sync"), NextDion: d.next(ctx, owner, "morning")}
	if recs, err := d.records(ctx); err == nil {
		for _, rec := range recs {
			if !delivery.Eligible(rec) {
				continue
			}
			if got, _ := d.redmine.Lookup(ctx, owner, delivery.SignalKey(rec)); got != nil {
				v.Issues = append(v.Issues, *got)
			} else {
				v.Waiting = append(v.Waiting, rec)
			}
		}
	}
	v.Draft, _ = d.dion.Latest(ctx, owner)
	if c, err := d.dion.Chat(ctx); err == nil && strings.TrimSpace(c) != "" {
		v.ChatSet = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	return deliveryPage.Execute(w, v)
}

var deliveryPage = template.Must(template.New("delivery").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Бэклог и Dion</title>
<style>
:root{--bg:#fff;--fg:#14161a;--muted:#5b6170;--line:#e3e6ec;--ok:#1a7f37;--warn:#b54708;--accent:#2f5bea}
@media (prefers-color-scheme:dark){:root{--bg:#121417;--fg:#eceef2;--muted:#9aa1ad;--line:#2a2f37;--ok:#4ac26b;--warn:#f0a35e;--accent:#7c9bff}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.45 system-ui,sans-serif}main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:1.5rem;margin:0 0 4px}h2{font-size:1.1rem;margin:0 0 10px}.muted{color:var(--muted)}section{border:1px solid var(--line);border-radius:14px;padding:18px;margin:16px 0}
.row{display:flex;gap:10px;align-items:center;justify-content:space-between;padding:8px 0;border-top:1px solid var(--line)}.row:first-of-type{border-top:0}
.ok{color:var(--ok);font-weight:600}.warn{color:var(--warn)}pre{white-space:pre-wrap;background:color-mix(in srgb,var(--line) 40%,transparent);border-radius:10px;padding:12px;font:14px/1.45 system-ui,sans-serif;margin:10px 0}
button{font:inherit;font-weight:600;border:1px solid var(--line);border-radius:9px;padding:8px 13px;background:transparent;color:var(--fg);cursor:pointer}
a{color:var(--accent)}form{display:inline}.tag{font-size:.8rem;border:1px solid var(--line);border-radius:999px;padding:2px 8px;color:var(--muted)}.err{border-color:var(--warn)}
</style><main>
<p class="muted"><a href="./">← К карточкам</a></p>
<h1>Бэклог и утренняя сводка</h1><p class="muted">Помощник сам сверяет сигналы с бэклогом Redmine и пишет продакту в Dion.</p>
{{if .Error}}<section class="err"><strong class="warn">Не получилось:</strong> {{.Error}}</section>{{end}}
<section><h2>Задачи в Redmine</h2>
{{range .Issues}}<div class="row"><span><span class="ok">#{{.IssueID}}</span> {{.Subject}}{{if .Noted}} <span class="tag">комментарий к существующей</span>{{else if .Reused}} <span class="tag">уже была</span>{{end}}</span><a href="{{.URL}}" target="_blank" rel="noopener">Открыть ↗</a></div>{{end}}
{{range .Waiting}}<div class="row"><span class="warn">{{.Hypothesis.Title}}</span><span class="muted">в очереди</span></div>{{end}}
{{if and (not .Issues) (not .Waiting)}}<p class="muted">Сигналов с проверенными цифрами пока нет.</p>{{end}}
<p class="muted">Следующая сверка: {{.NextSync}} <form method="post" action="./delivery/sync"><button>Сверить сейчас</button></form></p>
<p><span class="tag">сгенерирована ИИ-помощником</span> <span class="muted">— отдельный флаг и ID сигнала в полях задачи.</span></p></section>
<section><h2>Сводка в Dion</h2>
{{if not .ChatSet}}<p class="warn">Тестовый чат не задан (настройка dion_test_chat).</p>{{end}}
{{with .Draft}}<p class="muted">{{.Day}} · {{if .Sent}}<span class="ok">отправлено</span>{{else}}<span class="warn">не отправлено</span>{{end}}</p><pre>{{.Text}}</pre>{{else}}<p class="muted">Сегодня сводка ещё не уходила.</p>{{end}}
<p class="muted">Следующая: {{.NextDion}} <form method="post" action="./delivery/dion"><button>Отправить сейчас</button></form></p></section>
</main></html>`))
