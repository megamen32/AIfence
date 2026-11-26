package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"strings"
)

const browserAuthPath = "/__baserow/airlock-client-v2.js"

//go:embed airlock-client.js
var browserAuthJS []byte

type composedBody struct {
	io.Reader
	io.Closer
}

// The original Nuxt bundle hydrates after parsing the document. Its isolated
// bootstrap must run before hydration, including on a browser's first visit.
func addBrowserAuthentication(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return errors.New("unexpected encoding for native Baserow document")
	}
	reader := bufio.NewReaderSize(resp.Body, 8192)
	prefix, err := reader.Peek(8192)
	if err != nil && err != io.EOF {
		return err
	}
	open := bytes.Index(bytes.ToLower(prefix), []byte("<head>"))
	if open < 0 {
		return errors.New("native Baserow HTML head unavailable")
	}
	n := open + len("<head>")
	before := append([]byte(nil), prefix[:n]...)
	if _, err := reader.Discard(n); err != nil {
		return err
	}
	script := `<script src="` + browserAuthPath + `"></script><script defer data-airlock-disable-navigation="true" src="/__air/assets/web-ui-v1.js"></script>`
	resp.Body = composedBody{io.MultiReader(bytes.NewReader(before), strings.NewReader(script), reader), resp.Body}
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	resp.Header.Del("ETag")
	return nil
}
