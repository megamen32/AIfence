package main

import (
	"html/template"
	"net/http"
	"net/url"

	"github.com/airlockrun/agentsdk"
)

func main() { newAgent().Serve() }

var page = template.Must(template.New("baserow").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Baserow</title><style>html{color-scheme:light dark;font:14px system-ui}body{margin:0;display:flex;flex-direction:column;height:100dvh}header{display:flex;align-items:center;justify-content:space-between;padding:12px 16px;border-bottom:1px solid #8883}h1{font-size:16px;margin:0}a{color:#5275db}iframe{flex:1;width:100%;border:0}p{margin:12px 16px;color:#777}</style></head><body><header><h1>Baserow</h1><a href="{{.}}" target="_blank" rel="noopener noreferrer">Open in Baserow</a></header><iframe title="Baserow" src="{{.}}" referrerpolicy="no-referrer"></iframe></body></html>`))

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Reusable Baserow tables, filtered views and dashboards", Icon: "\U0001F4CB"})
	a.RegisterMCP(&agentsdk.MCP{Slug: "baserow", Name: "Baserow native MCP", URL: "http://baserow:80/mcp", AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingShared})
	view := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "browser_url", Description: "Authenticated Baserow page or approved shared-view URL, without secrets", Default: "http://localhost:42081"})
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Access: agentsdk.AccessUser, Description: "Open native Baserow interface", Handler: func(w http.ResponseWriter, r *http.Request) error {
		raw, e := view.Get(r.Context())
		if e != nil {
			return e
		}
		if !validURL(raw) {
			return agentsdk.NewHTTPError(400, "Configure a valid Baserow URL.", nil)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return page.Execute(w, raw)
	}})
	a.AddInstruction(&agentsdk.Instruction{Text: "Use native Baserow MCP to discover actual workspace tables, schemas and records. Never invent missing rows. The Market Watch catalog is an imported public-price projection, not authority to change actual store prices. Do not edit/delete observations; use separate user-owned notes and views. Do not perform destructive operations unless explicitly authorized. Answer in the user's language."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Browse tables", Prompt: "List the available Baserow databases and tables."})
	return a
}
