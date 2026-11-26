// catalogexport combines atomic snapshots into an Excel-ready UTF-8 CSV.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	catalog "agent/collector"
)

func copySource(path, id string, emit func(catalog.Offer) error) (int, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	decoder := json.NewDecoder(bufio.NewReader(f))
	count := 0
	for {
		var o catalog.Offer
		err := decoder.Decode(&o)
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		if o.Source != id || o.PriceMinor <= 0 || o.URL == "" || o.Currency == "" || o.Name == "" {
			return count, fmt.Errorf("invalid %s record %d", id, count+1)
		}
		if err = emit(o); err != nil {
			return count, err
		}
		count++
	}
}
func export(dir string, sources []string) (map[string]int, error) {
	counts := map[string]int{}
	target := filepath.Join(dir, "offers.csv")
	f, e := os.CreateTemp(dir, ".combined-*")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
		return nil, e
	}
	if e = catalog.CSVRows(f, func(emit func(catalog.Offer) error) error {
		for _, id := range sources {
			n, err := copySource(filepath.Join(dir, id, "offers.jsonl"), id, emit)
			if err != nil {
				return err
			}
			counts[id] = n
		}
		return nil
	}); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	return counts, os.Rename(f.Name(), target)
}

// Update on completion, review or a new generation, even when the ordinary
// coverage export is still fresh. Ordinary per-page updates do not trigger it.
func coverageDue(root string) bool {
	st, e := os.Stat(filepath.Join(root, "coverage.csv"))
	if e != nil || time.Since(st.ModTime()) >= 3*time.Hour {
		return true
	}
	var previous struct {
		Sources map[string]struct {
			Inventory string `json:"inventory_at"`
			State     string `json:"worker_state"`
		}
	}
	b, e := os.ReadFile(filepath.Join(root, "coverage-summary.json"))
	if e != nil || json.Unmarshal(b, &previous) != nil {
		return true
	}
	for _, id := range []string{"softline", "softmap", "softmagazin"} {
		name := "progress.json"
		if id == "softline" {
			name = "inventory.json"
		}
		b, e = os.ReadFile(filepath.Join(root, id, name))
		if e != nil {
			return false
		}
		var now struct {
			Inventory string `json:"inventory_at"`
			State     string `json:"state"`
			Complete  bool   `json:"complete"`
		}
		if json.Unmarshal(b, &now) != nil {
			return false
		}
		if id == "softline" {
			now.State = "running"
			if now.Complete {
				now.State = "complete"
			}
		}
		then, ok := previous.Sources[id]
		if !ok || now.Inventory != then.Inventory || now.State != then.State {
			return true
		}
	}
	return false
}
func main() {
	dir := flag.String("dir", "", "catalog root")
	sources := flag.String("sources", "softline,softmagazin,softmap", "comma-separated source IDs")
	flag.Parse()
	if *dir == "" {
		flag.Usage()
		os.Exit(2)
	}
	lock, e := os.OpenFile(filepath.Join(*dir, "export.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		panic(e)
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return
		}
		panic(e)
	}
	counts, e := export(*dir, strings.Split(*sources, ","))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(counts); e != nil {
		panic(e)
	}
	bin, e := os.Executable()
	if e != nil {
		panic(e)
	}
	checker := filepath.Join(filepath.Dir(bin), "market-coverage")
	if _, err := os.Stat(checker); err == nil {
		if coverageDue(*dir) {
			cmd := exec.Command(checker, "-catalog", *dir)
			cmd.Stderr = os.Stderr
			if e = cmd.Run(); e != nil {
				fmt.Fprintln(os.Stderr, e)
				os.Exit(1)
			}
		}
	}
}
