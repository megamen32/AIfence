package market

import (
	catalog "agent/collector"
	"github.com/airlockrun/agentsdk"

	"net/http"
)

func (s *Service) CSV(w http.ResponseWriter, r *http.Request) error {
	if s.queries != nil {
		return s.exportCatalog(w, r)
	}
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
