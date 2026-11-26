package market

import (
	catalog "agent/collector"
	"github.com/airlockrun/agentsdk"
	"html/template"
	"net/http"
)

var page = template.Must(template.New("market").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Market Watch</title><style>:root{color-scheme:light dark;font:14px system-ui}body{margin:24px}h1{font-size:22px}table{border-collapse:collapse;width:100%}th,td{text-align:left;border-bottom:1px solid #8885;padding:10px}small{opacity:.75}</style></head><body><h1>Market Watch</h1><p>Observed public prices - configured sample</p><p>{{.AsOf}} / {{.Status}} / <a href="catalog.csv">CSV for Excel</a></p>{{range .Warnings}}<p><small>{{.}}</small></p>{{end}}<table><thead><tr><th>Store</th><th>SKU</th><th>License</th><th>Price (RUB)</th><th>Qty</th></tr></thead><tbody>{{range .Observations}}<tr><td><a href="{{.SourceRef.URL}}" target="_blank" rel="noopener noreferrer">{{.Details.Seller}}</a></td><td>{{.ProductKey}}</td><td>{{.Details.Name}}</td><td>{{printf "%.2f" .Value}}</td><td>{{if .Details.Quantity}}{{.Details.Quantity}}{{else}}unknown{{end}}</td></tr>{{end}}</tbody></table>{{if not .Observations}}<p>No prices yet. Ask the chat to refresh prices.</p>{{end}}</body></html>`))

func (s *Service) Home(w http.ResponseWriter, r *http.Request) error {
	ev, e := s.List(r.Context(), ListInput{Limit: 100})
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return page.Execute(w, ev)
}
func (s *Service) CSV(w http.ResponseWriter, r *http.Request) error {
	snap, e := s.Snapshot(r.Context())
	if e != nil {
		return e
	}
	rows := snap.Rows()
	if len(rows) == 0 {
		return agentsdk.NewHTTPError(404, "No snapshot; refresh via chat first.", nil)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=market-watch.csv")
	if _, e = w.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
		return e
	}
	return catalog.CSV(w, rows)
}
