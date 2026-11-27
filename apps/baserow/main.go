package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type secretGetter interface {
	Get(context.Context) (string, error)
}
type service struct{ provision *provisioner }
type result struct {
	Data string `json:"data"`
}

func auth(ctx context.Context) error { _, e := currentUser(ctx); return e }
func (s *service) api(ctx context.Context, method, path string, body any) (result, error) {
	if e := auth(ctx); e != nil {
		return result{}, e
	}
	session, e := s.provision.session(ctx, false)
	if e != nil {
		return result{}, e
	}
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return result{}, e
		}
		reader = bytes.NewReader(b)
	}
	origin, e := s.provision.origin()
	if e != nil {
		return result{}, e
	}
	req, e := http.NewRequestWithContext(ctx, method, s.provision.base+path, reader)
	if e != nil {
		return result{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = origin.Host
	req.Header.Set("X-Forwarded-Proto", origin.Scheme)
	req.Header.Set("Authorization", "JWT "+session.Access)
	resp, e := safeNativeClient().Do(req)
	if e != nil {
		return result{}, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return result{}, e
	}
	if resp.StatusCode >= 300 {
		return result{}, fmt.Errorf("Baserow API HTTP %d", resp.StatusCode)
	}
	if len(b) > 20000 {
		b = b[:20000]
	}
	return result{Data: string(b)}, nil
}
func (s *service) ui(w http.ResponseWriter, r *http.Request) error {
	if e := auth(r.Context()); e != nil {
		http.Error(w, e.Error(), 403)
		return nil
	}
	appOrigin, e := s.provision.origin()
	if e != nil {
		return e
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != appOrigin.Scheme || !strings.EqualFold(u.Host, appOrigin.Host) {
			http.Error(w, "Cross-origin request denied", 403)
			return nil
		}
	}
	if r.URL.Path == browserAuthPath && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, err := w.Write(browserAuthJS)
		return err
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/user/token-auth" || (path == "/api/user" && r.Method != "GET") || path == "/api/user/token-blacklist" || strings.Contains(path, "/reset-password") || strings.Contains(path, "/change-password") || strings.Contains(path, "/change-email") || strings.Contains(path, "/delete-account") {
		http.Error(w, "Вход управляется Airlock", 403)
		return nil
	}
	session, e := s.provision.session(r.Context(), false)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return nil
	}
	if path == "/api/user/token-refresh" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(session.body)
		return nil
	}
	target, _ := url.Parse(s.provision.base)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = appOrigin.Host
			p.SetXForwarded()
			p.Out.Header.Set("X-Forwarded-Host", appOrigin.Host)
			p.Out.Header.Set("X-Forwarded-Proto", appOrigin.Scheme)
			for h := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(h), "x-airlock-") {
					p.Out.Header.Del(h)
				}
			}
			setNativeAuthentication(p.Out, session)
			if p.Out.Method == http.MethodGet && !strings.HasPrefix(p.Out.URL.Path, "/api/") && !strings.HasPrefix(p.Out.URL.Path, "/_nuxt/") {
				p.Out.Header.Set("Accept-Encoding", "identity")
			}

		},
		ModifyResponse: func(resp *http.Response) error {
			s.provision.acceptRotation(r.Context(), session, resp.Cookies())
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || strings.HasPrefix(r.URL.Path, "/api/") {
				resp.Header.Set("Cache-Control", "private, no-store")
			}
			resp.Header.Del("X-Frame-Options")
			resp.Header.Del("Set-Cookie")
			parent := dashboardOrigin(appOrigin)
			csp := resp.Header.Get("Content-Security-Policy")
			if csp != "" {
				parts := strings.Split(csp, ";")
				found := false
				for i, p := range parts {
					if strings.HasPrefix(strings.TrimSpace(p), "frame-ancestors ") {
						parts[i] = " frame-ancestors 'self' " + parent
						found = true
					}
				}
				if !found {
					parts = append(parts, " frame-ancestors 'self' "+parent)
				}
				resp.Header.Set("Content-Security-Policy", strings.Join(parts, ";"))
			} else {
				resp.Header.Set("Content-Security-Policy", "frame-ancestors 'self' "+parent)
			}
			if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, s.provision.base) {
				resp.Header.Set("Location", strings.TrimPrefix(loc, s.provision.base))
			}
			return addBrowserAuthentication(resp)
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
			http.Error(w, "Сервис недоступен", 502)
		},
	}
	proxy.ServeHTTP(w, r)
	return nil
}
func main() { newAgent().Serve() }
func routes(a *agentsdk.Agent, s *service) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		p := "/{path...}"
		if m == "GET" {
			p = "/"
		}
		a.RegisterRoute(&agentsdk.Route{Method: m, Path: p, Access: agentsdk.AccessUser, Logging: agentsdk.LoggingOff, Handler: s.ui, Description: "Native Baserow UI"})
	}
}

const upstream = "http://baserow:80"

func dashboardOrigin(u *url.URL) string {
	_, parent, found := strings.Cut(u.Hostname(), ".")
	if !found || parent == "" {
		return u.Scheme + "://" + u.Host
	}
	if u.Port() != "" {
		parent += ":" + u.Port()
	}
	return u.Scheme + "://" + parent
}

func appOrigin(a *agentsdk.Agent) (*url.URL, error) {
	raw, e := a.AgentURL()
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid Airlock app origin")
	}
	return u, nil
}

const tokenHeader = "Authorization"
const tokenPrefix = "Token "

func productCookie(s string) bool {
	return strings.HasPrefix(s, "baserow") || s == "jwt_token" || s == "refresh_token" || s == "locale"
}

type tableIn struct {
	TableID int `json:"table_id"`
}
type rowsIn struct {
	TableID int    `json:"table_id"`
	Search  string `json:"search,omitempty"`
}
type rowIn struct {
	TableID int `json:"table_id"`
	RowID   int `json:"row_id"`
}
type createIn struct {
	TableID int            `json:"table_id"`
	Fields  map[string]any `json:"fields"`
}

func (s *service) fields(c context.Context, i tableIn) (result, error) {
	if i.TableID < 1 {
		return result{}, errors.New("Укажите table_id")
	}
	return s.api(c, "GET", fmt.Sprintf("/api/database/fields/table/%d/", i.TableID), nil)
}
func (s *service) rows(c context.Context, i rowsIn) (result, error) {
	if i.TableID < 1 {
		return result{}, errors.New("Укажите table_id")
	}
	return s.api(c, "GET", fmt.Sprintf("/api/database/rows/table/%d/?user_field_names=true&size=30&search=%s", i.TableID, url.QueryEscape(i.Search)), nil)
}
func (s *service) row(c context.Context, i rowIn) (result, error) {
	if i.TableID < 1 || i.RowID < 1 {
		return result{}, errors.New("Укажите table_id и row_id")
	}
	return s.api(c, "GET", fmt.Sprintf("/api/database/rows/table/%d/%d/?user_field_names=true", i.TableID, i.RowID), nil)
}
func (s *service) create(c context.Context, i createIn) (result, error) {
	if i.TableID < 1 || len(i.Fields) == 0 {
		return result{}, errors.New("Укажите таблицу и поля")
	}
	return s.api(c, "POST", fmt.Sprintf("/api/database/rows/table/%d/?user_field_names=true", i.TableID), i.Fields)
}
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Таблицы и каталог: товары, цены и характеристики в Baserow.", Icon: "📋"})
	s := &service{provision: &provisioner{store: identityStore{db: a.DB()}, client: safeNativeClient(), base: upstream, origin: func() (*url.URL, error) { return appOrigin(a) }, sessions: make(map[string]nativeSession)}}
	routes(a, s)

	a.RegisterTool(tool.Typed[tableIn, result]("list_fields").Description("Прочитать схему таблицы Baserow по ID").Execute(s.fields).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[rowsIn, result]("list_rows").Description("Найти записи и товары по таблице и поисковой строке").Execute(s.rows).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[rowIn, result]("get_row").Description("Прочитать одну запись каталога по ID").Execute(s.row).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[createIn, result]("create_row").Description("Добавить запись в Baserow только по явному запросу владельца").Execute(s.create).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, result]("list_workspaces").Description("List workspaces of the authenticated Airlock user").Execute(func(c context.Context, _ struct{}) (result, error) { return s.api(c, "GET", "/api/workspaces/", nil) }).Build(), agentsdk.AccessUser)
	a.AddInstruction(&agentsdk.Instruction{Text: "Baserow is opened as the current Airlock user. Do not ask the user for a second login or a database token. Never edit observed store prices or perform destructive operations without an explicit request."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Поиск товара", Prompt: "Найди товар в указанной таблице Baserow и покажи точные характеристики."})
	return a
}
