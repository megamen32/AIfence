package views

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"sync"
)

//go:embed static/app.css
var AppCSS []byte

//go:embed static/explorer.js
var explorerJS []byte

//go:embed static/geo-catalog.json
var geoCatalogJSON []byte

func assetHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:6]) }

var AppCSSName = "app." + assetHash(AppCSS) + ".css"
var AppCSSPath = "/static/" + AppCSSName

// Catalog and interaction code are one immutable local asset. No runtime
// geographic lookup or external browser request is needed.
var AppJS = append(append(append([]byte("const GEO_CATALOG="), geoCatalogJSON...), []byte(";\n")...), explorerJS...)
var AppJSName = "explorer." + assetHash(AppJS) + ".js"
var AppJSPath = "/static/" + AppJSName

var geoNamesOnce sync.Once
var geoNames map[string]string

func GeoName(code string) string {
	geoNamesOnce.Do(func() {
		geoNames = map[string]string{}
		var rows [][]string
		if json.Unmarshal(geoCatalogJSON, &rows) == nil {
			for _, row := range rows {
				if len(row) >= 2 {
					geoNames[row[0]] = row[1]
				}
			}
		}
	})
	if name, ok := geoNames[code]; ok {
		return name
	}
	if code == "" {
		return "Весь мир"
	}
	return code
}
