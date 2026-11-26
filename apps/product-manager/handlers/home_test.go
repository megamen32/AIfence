package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent/internal/evidence"
)

func TestApprovalIsExplicitAndBoundToDisplayedVersion(t *testing.T) {
	cases := []string{"id=fixture", "id=fixture&confirmed=yes", "id=fixture&confirmed=yes&version=0"}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/approve?confirmed=yes&version=1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		if err := (&Handler{}).Approve(rec, req); err != nil || rec.Code != 400 {
			t.Fatal(body, rec.Code, err)
		}
	}
}
func TestImportRejectsOversizeBeforeService(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/import", strings.NewReader(strings.Repeat("x", evidence.MaxSize+1)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := (&Handler{}).Import(w, r); err != nil || w.Code != 413 {
			t.Fatal(w.Code, err)
		}
	})
	t.Run("file", func(t *testing.T) {
		var data bytes.Buffer
		form := multipart.NewWriter(&data)
		file, err := form.CreateFormFile("file", "sample.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(bytes.Repeat([]byte("x"), evidence.MaxSize+1)); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/import", &data)
		r.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		if err := (&Handler{}).Import(w, r); err != nil || w.Code != 413 {
			t.Fatal(w.Code, err)
		}
	})
	t.Run("wrong mime", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/import", strings.NewReader("x"))
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		if err := (&Handler{}).Import(w, r); err != nil || w.Code != 415 {
			t.Fatal(w.Code, err)
		}
	})
}
func TestNumericPresentationRespectsMissingAndUnits(t *testing.T) {
	r := evidence.Row{Product: "sample", Metric: "test", Value: "0.3125", Unit: "ratio", Status: "observed", Ref: evidence.SourceRef{URL: "javascript:alert(1)"}}
	got := rowView("sample", r)
	if got.Value != "31,2 %" || got.SourceURL != "" {
		t.Fatal(got)
	}
	r.Value = "0"
	if got = rowView("sample", r); got.Value != "0,0 %" {
		t.Fatal(got)
	}
	r.Status = "missing"
	r.Value = "null"
	if got = rowView("sample", r); !got.Missing || got.Value == "0,0 %" {
		t.Fatal(got)
	}
}
