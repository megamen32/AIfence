package market

import (
	catalog "agent/collector"
	"bytes"
	"context"
	"errors"
	"github.com/airlockrun/agentsdk"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type memoryStore map[string][]byte

func (m memoryStore) ReadFile(_ context.Context, p string) ([]byte, error) {
	b, ok := m[p]
	if !ok {
		return nil, agentsdk.ErrNotFound
	}
	return bytes.Clone(b), nil
}
func (m memoryStore) WriteFile(_ context.Context, p string, r io.Reader, _ string) (agentsdk.FileInfo, error) {
	b, e := io.ReadAll(r)
	if e == nil {
		m[p] = b
	}
	return agentsdk.FileInfo{}, e
}
func (m memoryStore) ListDir(_ context.Context, p string, _ agentsdk.ListOpts) ([]agentsdk.FileInfo, error) {
	var out []agentsdk.FileInfo
	for k := range m {
		if strings.HasPrefix(k, p+"/") {
			out = append(out, agentsdk.FileInfo{Path: agentsdk.FilePath(k), Filename: strings.TrimPrefix(k, p+"/")})
		}
	}
	return out, nil
}
func TestRefreshAndPreserve(t *testing.T) {
	store := memoryStore{}
	s := New(store)
	s.now = func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) }
	s.fetch = func(_ context.Context, src catalog.Source, _ string) ([]byte, error) {
		h := map[string]string{
			"softmagazin": `<div class="offers"><span class="articul" title="KL4141RCEFS"></span><a class="name" href="/vendor/item/">Test</a><span class="price" content="7488"></span></div>`,
			"softmap":     `<div class="table-tr"><span class="art">KL4141RCEFS</span><div class="div-h3"><a href="/vendor/item/">Test</a></div><div class="item-count"><input value="1"></div><div class="new-price"><span>6739</span></div></div>`,
			"softline":    `<html><body><button class="js-sku-btn" data-code="KL4141RCEFS"></button><h1>Test</h1><span class="js-price-with-discount">6739.20</span></body></html>`,
		}
		return []byte(h[src.ID]), nil
	}
	ev, e := s.Refresh(t.Context(), RefreshInput{ProductKey: "KL4141RCEFS"})
	if e != nil || ev.Status != "ok" || len(ev.Observations) != 3 || len(store["catalog/latest.csv"]) == 0 {
		t.Fatalf("%+v %v", ev, e)
	}
	s.fetch = func(context.Context, catalog.Source, string) ([]byte, error) { return nil, errors.New("unavailable") }
	ev, e = s.Refresh(t.Context(), RefreshInput{ProductKey: "KL4141RCEFS"})
	if e != nil || ev.Status != "partial" || len(ev.Observations) != 3 {
		t.Fatalf("%+v %v", ev, e)
	}
}
func TestLiveSample(t *testing.T) {
	if os.Getenv("AIFENCE_LIVE_SMOKE") != "1" {
		t.Skip("explicit live smoke only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	s := New(memoryStore{})
	ev, e := s.Refresh(ctx, RefreshInput{ProductKey: "KL4141RCEFS"})
	if e != nil || len(ev.Observations) != 3 || ev.Status != "ok" {
		t.Fatalf("%+v %v", ev, e)
	}
	for _, o := range ev.Observations {
		t.Logf("%s %s %.2f RUB %s", o.Details.Seller, o.ProductKey, o.Value, o.SourceRef.URL)
	}
}
