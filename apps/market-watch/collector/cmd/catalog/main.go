// Standalone smoke-test/worker CLI. Airlock owns the production schedule.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	catalog "agent/collector"
)

func atomic(path string, write func(*os.File) error) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = write(f); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func main() {
	config := flag.String("config", "sources.json", "source configuration")
	out := flag.String("out", "/tmp/aifence-catalog", "output directory")
	discover := flag.Bool("discover", false, "enumerate sitemap candidates")
	limit := flag.Int("max-pages", 10, "page budget per source")
	timeout := flag.Duration("timeout", 5*time.Minute, "run timeout")
	flag.Parse()
	f, e := os.Open(*config)
	if e != nil {
		panic(e)
	}
	sources, e := catalog.ReadSources(f)
	f.Close()
	if e != nil {
		panic(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	http := catalog.NewHTTP()
	at := time.Now().UTC()
	stamp := at.Format("20060102T150405.000000000Z")
	type result struct {
		s catalog.Source
		r catalog.Run
	}
	ch := make(chan result, len(sources))
	for _, s := range sources {
		go func(s catalog.Source) { ch <- result{s, catalog.Collect(ctx, s, http.Fetch, *discover, *limit, at)} }(s)
	}
	failed := false
	for range sources {
		item := <-ch
		r := item.r
		dir := filepath.Join(*out, item.s.ID)
		// Every run gets a manifest, including failures and bounded samples.
		if e = atomic(filepath.Join(dir, "runs", stamp+".json"), func(f *os.File) error { return json.NewEncoder(f).Encode(r) }); e != nil {
			panic(e)
		}
		if len(r.Offers) > 0 {
			save := func(f *os.File) error {
				if _, e := f.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
					return e
				}
				return catalog.CSV(f, r.Offers)
			}
			if e = atomic(filepath.Join(dir, "runs", stamp+".csv"), save); e != nil {
				panic(e)
			}
			// Never replace complete catalog with sample, partial or limited data.
			if r.Status == "ok" && r.Mode == "sitemap" {
				if e = atomic(filepath.Join(dir, "catalog.csv"), save); e != nil {
					panic(e)
				}
			}
			if r.Status == "ok" && r.Mode == "sample" {
				if e = atomic(filepath.Join(dir, "sample.csv"), save); e != nil {
					panic(e)
				}
			}
		}
		fmt.Printf("%s %s %s offers=%d pages=%d/%d empty=%d errors=%d\n", item.s.ID, r.Mode, r.Status, len(r.Offers), r.Pages, r.CandidatePages, r.EmptyPages, len(r.Errors))
		if r.Status == "failed" || r.Status == "partial" {
			failed = true
			for _, v := range r.Errors {
				fmt.Fprintln(os.Stderr, v)
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}
