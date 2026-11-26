package views

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

//go:embed static/app.css
var AppCSS []byte

var AppCSSName = fmt.Sprintf("market.%x.css", sha256.Sum256(AppCSS))
