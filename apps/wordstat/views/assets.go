package views

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

//go:embed static/app.css
var AppCSS []byte

//go:embed static/explorer.js
var explorerJS []byte

//go:embed static/region-catalog.json
var regionCatalog []byte

func assetHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:6]) }

var AppCSSName = "app." + assetHash(AppCSS) + ".css"
var AppCSSPath = "/static/" + AppCSSName
var AppJS = append(append([]byte("const REGION_CATALOG="), regionCatalog...), append([]byte(";\n"), explorerJS...)...)
var AppJSName = "explorer." + assetHash(AppJS) + ".js"
var AppJSPath = "/static/" + AppJSName
var regionOnce sync.Once
var regionNames map[string]string

func RegionName(id string) string {
	if id == "" {
		return "Все регионы"
	}
	regionOnce.Do(func() {
		regionNames = map[string]string{}
		var rows [][]string
		if json.Unmarshal(regionCatalog, &rows) == nil {
			for _, row := range rows {
				if len(row) > 1 {
					regionNames[row[0]] = row[1]
				}
			}
		}
	})
	if name := regionNames[id]; name != "" {
		return name
	}
	return "Регион " + id
}
func RegionCaption(ids []int) string {
	if len(ids) == 0 {
		return "Все регионы"
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, RegionName(strconv.Itoa(id)))
	}
	return strings.Join(names, ", ")
}
