// Command coverageimport prints a local coverage report or submits it to the
// selected Market Watch app through the official SDK.
package main

import (
	"agent/internal/crawlreport"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func run() error {
	root := flag.String("root", "../../.runtime/market-catalog", "local market-catalog directory")
	upload := flag.Bool("upload", false, "submit the report via the official SDK")
	var target crawlreport.Target
	flag.StringVar(&target.CLI, "air", "air", "official SDK CLI executable")
	flag.StringVar(&target.AppDir, "app-dir", ".", "Market Watch checkout")
	flag.StringVar(&target.Origin, "url", crawlreport.LocalOrigin, "selected Airlock API origin")
	flag.StringVar(&target.Agent, "agent", crawlreport.LocalAgent, "deployed Market Watch app slug or ID")
	flag.Parse()
	data, err := crawlreport.Build(*root)
	if err != nil {
		return err
	}
	if *upload {
		if err := crawlreport.Upload(context.Background(), data, target); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(data)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
