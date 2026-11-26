package main

import (
	"agent/internal/product"
	"encoding/json"
	"net/http"
	"net/url"
)

func investigatePage(p product.Service) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Некорректная форма", 400)
			return nil
		}
		rec, err := p.Investigate(r.Context(), product.InvestigateInput{
			SnapshotID: r.PostForm.Get("snapshot_id"), SignalID: r.PostForm.Get("signal_id"),
			ProductKey: r.PostForm.Get("product_key"), WeekFrom: r.PostForm.Get("week_from"),
		})
		if err != nil {
			http.Error(w, err.Error(), 422)
			return nil
		}
		if r.Header.Get("Accept") == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			return json.NewEncoder(w).Encode(rec)
		}
		http.Redirect(w, r, "/?id="+url.QueryEscape(rec.Hypothesis.ID), http.StatusSeeOther)
		return nil
	}
}
