//go:build tools

package main

// The templ tool directive in go.mod needs this import so `go mod tidy` keeps
// the same dependency classification before and after the templates are
// generated. Without it a fresh clone would rewrite go.mod on the first build.
import _ "github.com/a-h/templ"
