package catalog

import (
	"bytes"
	_ "embed"
)

//go:embed sources.json
var config []byte

func Sources() ([]Source, error) { return ReadSources(bytes.NewReader(config)) }
