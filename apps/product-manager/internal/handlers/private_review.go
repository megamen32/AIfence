// Package handlers provides the signed-in, browser-only private review page.
package handlers

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"agent/internal/evidence"
	"agent/views"
	"github.com/a-h/templ"
	"github.com/airlockrun/agentsdk"
)

const privatePageSize = 40

type privateEntry struct {
	Kind, ID, Title, Date string
	Count                 int
}

func (e privateEntry) Href() string {
	key := "id"
	if e.Kind == "snapshot" {
		key = "snapshot"
	}
	return "./?" + url.Values{key: {e.ID}}.Encode()
}

type privateSelection func(context.Context, string, int) ([]privateEntry, error)

// PrivateReviewPage must be registered as AccessUser under evidence.LocalHTTP.
func PrivateReviewPage(db *agentsdk.AgentDB) agentsdk.RouteHandlerFunc {
	return privatePage(func(ctx context.Context, owner string, offset int) ([]privateEntry, error) {
		// Apply the owner and visibility filters before pagination. Only the metadata
		// displayed on this page leaves PostgreSQL; source documents stay in place.
		rows, err := db.QueryContext(ctx, `
WITH private_sources AS (
	SELECT snapshot_id, as_of, observations, imported_at
	FROM pm_evidence
	WHERE user_id = $1::uuid AND document->>'source_policy' = 'local_project_only'
)
SELECT kind, id, title, as_of, observations FROM (
	SELECT 'card' AS kind, d.hypothesis_id AS id,
		COALESCE(NULLIF(d.body->'hypothesis'->>'title',''), d.hypothesis_id) AS title,
		e.as_of, 0 AS observations, d.updated_at AS recent
	FROM pm_decisions d
	JOIN private_sources e ON e.snapshot_id = d.body->>'evidence_snapshot_id'
	WHERE d.owner_id = $1::uuid::text
	UNION ALL
	SELECT 'snapshot', snapshot_id, '', as_of, observations, imported_at FROM private_sources
) items ORDER BY recent DESC, kind, id LIMIT $2 OFFSET $3`, owner, privatePageSize+1, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		entries := make([]privateEntry, 0, privatePageSize+1)
		for rows.Next() {
			var e privateEntry
			if err := rows.Scan(&e.Kind, &e.ID, &e.Title, &e.Date, &e.Count); err != nil {
				return nil, err
			}
			e.Date, _, _ = strings.Cut(e.Date, "T")
			entries = append(entries, e)
		}
		return entries, rows.Err()
	})
}

type privateView struct {
	Cards, Snapshots []privateEntry
	Previous, Next   int
}

func privatePage(selectRows privateSelection) agentsdk.RouteHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		ctx := r.Context()
		user, ok := agentsdk.CallerFromContext(ctx).User()
		if !evidence.IsLocalHTTP(ctx) || !ok || user.ID == "" {
			http.Error(w, "Откройте страницу в своей учётной записи Airlock.", http.StatusForbidden)
			return nil
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Требуется GET.", http.StatusMethodNotAllowed)
			return nil
		}
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000000 {
				http.Error(w, "Некорректная страница.", http.StatusBadRequest)
				return nil
			}
			page = n
		}
		entries, err := selectRows(ctx, user.ID, (page-1)*privatePageSize)
		if err != nil {
			http.Error(w, "Не удалось загрузить материалы. Обновите страницу.", http.StatusInternalServerError)
			return nil
		}
		view := privateView{Previous: page - 1}
		if len(entries) > privatePageSize {
			view.Next = page + 1
			entries = entries[:privatePageSize]
		}
		for _, entry := range entries {
			switch entry.Kind {
			case "card":
				view.Cards = append(view.Cards, entry)
			case "snapshot":
				view.Snapshots = append(view.Snapshots, entry)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		content := templ.ComponentFunc(func(_ context.Context, out io.Writer) error { return privateBoard.Execute(out, view) })
		return views.Layout("Мои закрытые материалы").Render(templ.WithChildren(ctx, content), w)
	}
}

var privateBoard = template.Must(template.New("private-board").Parse(`
<h1 class="headline">Мои закрытые материалы</h1>
<section class="section-block" aria-labelledby="cards">
  <h2 id="cards">Карточки решений</h2>
  {{range .Cards}}<a class="nav-item" href="{{.Href}}"><strong>{{.Title}}</strong><small>Данные на {{.Date}}</small></a>{{else}}<p class="muted">На этой странице карточек нет.</p>{{end}}
</section>
<section class="section-block" aria-labelledby="snapshots">
  <h2 id="snapshots">Снимки данных</h2>
  {{range .Snapshots}}<a class="nav-item" href="{{.Href}}"><strong>Данные на {{.Date}}</strong><small>Наблюдений: {{.Count}} · {{.ID}}</small></a>{{else}}<p class="muted">На этой странице снимков нет.</p>{{end}}
</section>
{{if or .Previous .Next}}<nav class="paging" aria-label="Страницы закрытых материалов">{{if .Previous}}<a rel="prev" href="?page={{.Previous}}">Назад</a>{{end}} {{if .Next}}<a rel="next" href="?page={{.Next}}">Дальше</a>{{end}}</nav>{{end}}
`))
