package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNativeBrowserBootstrapBeforeNuxt(t *testing.T) {
	html := "<!DOCTYPE html><html><head><script type=module src='/_nuxt/app.js'></script></head><body>test</body></html>"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}, "Content-Length": {"123"}, "Etag": {"previous"}}, Body: io.NopCloser(strings.NewReader(html))}
	if err := addBrowserAuthentication(resp); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(raw), `<head><script src="`+browserAuthPath+`"></script><script defer data-airlock-disable-navigation="true" src="/__air/assets/web-ui-v1.js"></script><script type=module`) || resp.Header.Get("Content-Length") != "" || resp.Header.Get("ETag") != "" {
		t.Fatal("native browser bootstrap not placed ahead of Nuxt")
	}
}
