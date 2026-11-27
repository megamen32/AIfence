package main

import (
	"context"
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

// customerDelivery wires the owner-approved writes into the customer's tools:
// a Redmine issue after "Создать эту задачу" and a morning Dion message that
// UserIO stores as a draft until the owner presses "Отправить в Dion".
// Neither path is reachable from chat or a model tool.
type customerDelivery struct {
	p       product.Service
	redmine *delivery.Redmine
	dion    *delivery.Dion
	morning *agentsdk.JobHandle[morningInput, morningOutput]
	project *agentsdk.EnvVarHandle
	public  *agentsdk.EnvVarHandle
}

type morningInput struct {
	Day string `json:"day"`
}
type morningOutput struct {
	Day     string `json:"day"`
	DraftID string `json:"draft_id,omitempty"`
	Status  string `json:"status"`
}

const (
	redmineUpstream = "http://aifence-personal-redmine:3000"
	userioMCP       = "https://msg.bezrabotnyi.com/mcp"
	// 04:00 UTC is 09:00 at the owner's UTC+5 office.
	morningHourUTC = 4
)

func registerCustomerDelivery(a *agentsdk.Agent, p product.Service) *customerDelivery {
	conn := a.RegisterConnection(&agentsdk.Connection{
		Slug: "redmine", Name: "Redmine", Description: "Трекер задач заказчика. Product Manager создаёт черновик задачи только после подтверждения карточки владельцем.",
		BaseURL: redmineUpstream, AuthMode: agentsdk.ConnectionAuthToken,
		AuthInjection: agentsdk.AuthInjection{Type: agentsdk.AuthInjectAPIKey, Name: "X-Redmine-API-Key"},
		BindingMode:   agentsdk.BindingShared,
		SetupInstructions: "Вставьте API-ключ служебного пользователя Redmine «ИИ помощник» (Моя учётная запись → Ключ доступа к API). " +
			"Пользователь должен быть участником проекта из настройки redmine_project.",
	})
	mcp := a.RegisterMCP(&agentsdk.MCP{Slug: "userio", Name: "UserIO · Dion", URL: userioMCP, AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingPerUser})
	project := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "redmine_project", Description: "Идентификатор проекта Redmine для черновиков задач.", Default: "personal", Pattern: `^[a-z0-9][a-z0-9_-]{0,99}$`})
	public := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "redmine_public_url", Description: "Адрес Redmine для ссылок в панели.", Default: "http://redmine.localhost:42080", Pattern: `^https?://[^\s/]+(/[^\s]*)?$`})
	chat := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "dion_test_chat", Description: "ID тестового чата Dion в UserIO (conv_…). Сообщения уходят только в него; пусто = отправка выключена.", Default: "", Pattern: `^(|conv_[A-Za-z0-9]{8,64})$`})
	d := &customerDelivery{p: p, project: project, public: public}
	d.redmine = &delivery.Redmine{Conn: conn, DB: a.DB(), Tracker: "Улучшение"}
	d.dion = &delivery.Dion{MCP: mcp, DB: a.DB(), Chat: chat.Get}
	d.morning = agentsdk.RegisterJob(a, &agentsdk.Job[morningInput, morningOutput]{
		Name: "prepare_morning_dion", Version: 1,
		Description: "Prepare the owner's morning top-deviation message as a UserIO draft for the configured Dion test chat. Never sends.",
		Timeout:     2 * time.Minute, MaxAttempts: 3, MaxConcurrency: 1, Handler: d.runMorning,
	})
	for _, r := range []*agentsdk.Route{
		{Method: "GET", Path: "/delivery", Handler: d.page, Description: "Доставка в Redmine и Dion"},
		{Method: "POST", Path: "/delivery/redmine", Handler: confirmed(d.retryRedmine), Description: "Повторить отправку подтверждённой задачи в Redmine"},
		{Method: "POST", Path: "/delivery/dion/prepare", Handler: confirmed(d.prepareDion), Description: "Подготовить утреннее сообщение в Dion"},
		{Method: "POST", Path: "/delivery/dion/send", Handler: confirmed(d.sendDion), Description: "Отправить подготовленное сообщение в тестовый чат Dion"},
		{Method: "POST", Path: "/delivery/dion/schedule", Handler: confirmed(d.scheduleDion), Description: "Готовить сообщение в Dion каждое утро"},
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

// AfterApprove runs once the local backlog task exists. A Redmine failure is
// logged and leaves the card linked to /delivery for an explicit retry.
func (d *customerDelivery) AfterApprove(ctx context.Context, r decision.Record) {
	owner, err := caller(ctx)
	if err != nil {
		return
	}
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
	if err != nil {
		return "", "", false
	}
	if got == nil {
		return r.Task.ID + " · отправить в Redmine", "./delivery?id=" + url.QueryEscape(r.Hypothesis.ID), true
	}
	return "Redmine #" + strconv.Itoa(got.IssueID), got.URL, true
}

func confirmed(next agentsdk.RouteHandlerFunc) agentsdk.RouteHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				http.Error(w, "Подтвердите действие в панели Product Manager.", http.StatusForbidden)
				return nil
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := r.ParseForm(); err != nil || r.PostForm.Get("confirmed") != "yes" {
			http.Error(w, "Нужно явное подтверждение.", http.StatusBadRequest)
			return nil
		}
		return next(w, r)
	}
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

func (d *customerDelivery) retryRedmine(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	id := r.PostForm.Get("id")
	rec, err := d.p.Get(r.Context(), decision.GetInput{HypothesisID: id})
	if err != nil {
		return err
	}
	d.configure(r.Context())
	if _, err := d.redmine.Deliver(r.Context(), owner, rec); err != nil {
		return d.render(w, r, err.Error())
	}
	http.Redirect(w, r, "./?id="+url.QueryEscape(id), http.StatusSeeOther)
	return nil
}

func (d *customerDelivery) prepareDion(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	recs, err := d.records(r.Context())
	if err != nil {
		return err
	}
	if _, err := d.dion.Prepare(r.Context(), owner, time.Now(), recs); err != nil {
		return d.render(w, r, err.Error())
	}
	http.Redirect(w, r, "./delivery", http.StatusSeeOther)
	return nil
}

func (d *customerDelivery) sendDion(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	if _, err := d.dion.Send(r.Context(), owner, r.PostForm.Get("day"), time.Now()); err != nil {
		return d.render(w, r, err.Error())
	}
	http.Redirect(w, r, "./delivery", http.StatusSeeOther)
	return nil
}

func nextMorning(now time.Time) time.Time {
	t := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), morningHourUTC, 0, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

func morningJobID(owner string, at time.Time) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("aifence/morning-dion/"+owner+"/"+at.Format("2006-01-02"))).String()
}

func (d *customerDelivery) scheduleDion(w http.ResponseWriter, r *http.Request) error {
	owner, err := caller(r.Context())
	if err != nil {
		return err
	}
	at := nextMorning(time.Now())
	if _, err := d.morning.EnqueueAt(r.Context(), morningJobID(owner, at), at, morningInput{Day: at.Add(5 * time.Hour).Format("2006-01-02")}); err != nil {
		return d.render(w, r, err.Error())
	}
	http.Redirect(w, r, "./delivery", http.StatusSeeOther)
	return nil
}

func (d *customerDelivery) runMorning(ctx context.Context, job agentsdk.JobContext, in morningInput) (morningOutput, error) {
	owner, err := caller(ctx)
	if err != nil {
		return morningOutput{}, err
	}
	out := morningOutput{Day: in.Day, Status: "prepared"}
	recs, err := d.records(ctx)
	if err == nil {
		var dr delivery.DionDraft
		dr, err = d.dion.Prepare(ctx, owner, time.Now(), recs)
		out.DraftID = dr.DraftID
	}
	if err != nil {
		out.Status = "failed: " + err.Error()
	}
	// Keep the daily rhythm even after a failed morning.
	at := nextMorning(time.Now())
	if _, qerr := d.morning.EnqueueAt(ctx, morningJobID(owner, at), at, morningInput{Day: at.Add(5 * time.Hour).Format("2006-01-02")}); qerr != nil {
		slog.WarnContext(ctx, "Product Manager: next morning Dion draft not queued", "error", qerr.Error())
	}
	return out, nil
}

type deliveryView struct {
	Error    string
	Issues   []delivery.RedmineIssue
	Pending  []decision.Record
	Draft    *delivery.DionDraft
	ChatSet  bool
	NextRun  string
	Redmine  string
	Selected string
}

func (d *customerDelivery) page(w http.ResponseWriter, r *http.Request) error {
	return d.render(w, r, "")
}

func (d *customerDelivery) render(w http.ResponseWriter, r *http.Request, msg string) error {
	ctx := r.Context()
	owner, err := caller(ctx)
	if err != nil {
		return err
	}
	d.configure(ctx)
	v := deliveryView{Error: msg, Redmine: d.redmine.PublicURL, Selected: r.URL.Query().Get("id"), NextRun: nextMorning(time.Now()).Add(5 * time.Hour).Format("02.01 15:04")}
	if recs, err := d.records(ctx); err == nil {
		for _, rec := range recs {
			if rec.Task == nil {
				continue
			}
			if got, _ := d.redmine.Lookup(ctx, owner, delivery.SignalKey(rec)); got != nil {
				v.Issues = append(v.Issues, *got)
			} else {
				v.Pending = append(v.Pending, rec)
			}
		}
	}
	v.Draft, _ = d.dion.Latest(ctx, owner)
	if c, err := d.dion.Chat(ctx); err == nil && strings.TrimSpace(c) != "" {
		v.ChatSet = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if msg != "" {
		w.WriteHeader(http.StatusBadGateway)
	}
	return deliveryPage.Execute(w, v)
}

var deliveryPage = template.Must(template.New("delivery").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Доставка задач</title>
<style>
:root{--bg:#fff;--fg:#14161a;--muted:#5b6170;--line:#e3e6ec;--ok:#1a7f37;--warn:#b54708;--accent:#2f5bea}
@media (prefers-color-scheme:dark){:root{--bg:#121417;--fg:#eceef2;--muted:#9aa1ad;--line:#2a2f37;--ok:#4ac26b;--warn:#f0a35e;--accent:#7c9bff}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.45 system-ui,sans-serif}main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:1.5rem;margin:0 0 4px}h2{font-size:1.1rem;margin:0 0 10px}.muted{color:var(--muted)}section{border:1px solid var(--line);border-radius:14px;padding:18px;margin:16px 0}
.row{display:flex;gap:10px;align-items:center;justify-content:space-between;padding:8px 0;border-top:1px solid var(--line)}.row:first-of-type{border-top:0}
.ok{color:var(--ok);font-weight:600}.warn{color:var(--warn)}pre{white-space:pre-wrap;background:color-mix(in srgb,var(--line) 40%,transparent);border-radius:10px;padding:12px;font:14px/1.45 system-ui,sans-serif;margin:10px 0}
button{font:inherit;font-weight:600;border:0;border-radius:9px;padding:9px 14px;background:var(--accent);color:#fff;cursor:pointer}button.secondary{background:transparent;color:var(--fg);border:1px solid var(--line)}
a{color:var(--accent)}form{display:inline}.tag{font-size:.8rem;border:1px solid var(--line);border-radius:999px;padding:2px 8px;color:var(--muted)}.err{border-color:var(--warn)}
</style><main>
<p class="muted"><a href="./">← К карточкам</a></p>
<h1>Доставка в Redmine и Dion</h1><p class="muted">Запись в системы заказчика выполняется только по вашей кнопке.</p>
{{if .Error}}<section class="err"><strong class="warn">Не получилось:</strong> {{.Error}}</section>{{end}}
<section><h2>Задачи в Redmine</h2>
{{range .Issues}}<div class="row"><span><span class="ok">#{{.IssueID}}</span> {{.Subject}}</span><a href="{{.URL}}" target="_blank" rel="noopener">Открыть ↗</a></div>{{end}}
{{range .Pending}}<div class="row"><span class="warn">{{.Hypothesis.Title}} · ещё не в Redmine</span><form method="post" action="./delivery/redmine"><input type="hidden" name="id" value="{{.Hypothesis.ID}}"><button name="confirmed" value="yes">Отправить в Redmine</button></form></div>{{end}}
{{if and (not .Issues) (not .Pending)}}<p class="muted">Подтверждённых задач пока нет. Создайте задачу на карточке сигнала.</p>{{end}}
<p><span class="tag">сгенерирована ИИ-помощником</span> <span class="muted">— метка и ID сигнала ставятся в полях задачи.</span></p></section>
<section><h2>Утреннее сообщение в Dion</h2>
{{if not .ChatSet}}<p class="warn">Тестовый чат не задан (настройка dion_test_chat).</p>{{end}}
{{with .Draft}}<p class="muted">Черновик на {{.Day}}{{if .Sent}} · <span class="ok">отправлено {{.SentAt}}</span>{{else}} · ждёт подтверждения{{end}}</p><pre>{{.Text}}</pre>
{{if not .Sent}}<form method="post" action="./delivery/dion/send"><input type="hidden" name="day" value="{{.Day}}"><button name="confirmed" value="yes">Отправить в Dion</button></form>{{end}}{{else}}<p class="muted">Сегодняшний черновик ещё не готов.</p>{{end}}
<p><form method="post" action="./delivery/dion/prepare"><button class="secondary" name="confirmed" value="yes">Подготовить сейчас</button></form>
<form method="post" action="./delivery/dion/schedule"><button class="secondary" name="confirmed" value="yes">Готовить каждое утро (следующее {{.NextRun}})</button></form></p>
<p class="muted">UserIO хранит черновик; сообщение уходит только после «Отправить в Dion».</p></section>
</main></html>`))
