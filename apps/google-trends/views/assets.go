package views

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed static/app.css
var AppCSS []byte

func AppCSSHash() string { h := sha256.Sum256(AppCSS); return hex.EncodeToString(h[:4]) }

var AppCSSName = "app." + AppCSSHash() + ".css"
var AppCSSPath = "/static/" + AppCSSName
