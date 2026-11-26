// Command importcatalog uploads new public observations from the local collector
// through an authenticated Airlock admin route. Replays are idempotent.
package main

import (
	"agent/internal/crawlreport"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gofrs/flock"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type uploader struct {
	air, dir, origin, agent string
	uploaded                map[string]bool
	state                   *os.File
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", "", "directory containing store exports")
	dir := flag.String("app-dir", ".", "Market Watch app checkout with the local Airlock binding")
	air := flag.String("air", "air", "official Airlock SDK CLI executable")
	url := flag.String("url", "http://localhost:42080", "selected Airlock API origin")
	agent := flag.String("agent", "market-watch", "deployed application slug or ID")
	statePath := flag.String("checkpoint", "", "private checkpoint file for this instance and app")
	flag.Parse()
	if *root == "" || *statePath == "" {
		return errors.New("-root and -checkpoint are required")
	}
	files, err := filepath.Glob(filepath.Join(*root, "*", "offers.jsonl"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no per-store offers.jsonl exports available")
	}
	// Both the initial installation and the normal price update can invoke this command.
	lock := flock.New(*statePath + ".lock")
	owned, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("another Market Watch import is active for this checkpoint")
	}
	defer lock.Unlock()
	file, err := os.OpenFile(*statePath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	u := uploader{*air, *dir, *url, *agent, make(map[string]bool), file}
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		u.uploaded[scan.Text()] = true
	}
	if err := scan.Err(); err != nil {
		return err
	}
	for _, p := range files {
		count, err := u.importFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		fmt.Printf("%s: %d new observations uploaded\n", filepath.Base(filepath.Dir(p)), count)
	}
	target := crawlreport.Target{CLI: u.air, AppDir: u.dir, Origin: u.origin, Agent: u.agent}
	report, err := crawlreport.WithPrices(context.Background(), *root, target)
	if err != nil {
		return fmt.Errorf("price checkpoints saved; coverage import: %w", err)
	}
	if report.AsOf != "" {
		fmt.Printf("Sitemap coverage imported at %s\n", report.AsOf)
	}
	return nil
}

func (u *uploader) importFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 65536), 4<<20)
	var rows []json.RawMessage
	var keys []string
	size, count := 0, 0
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		b, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, u.air, "request", "POST", "/import", "--dir", u.dir, "--remote", "default", "--target", "production", "--url", u.origin, "--agent", u.agent, "--header", "Content-Type: application/json", "--data", string(b))
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("Airlock request: %w: %.1000s", err, diagnostics.Bytes())
		}
		var result struct {
			Accepted int `json:"accepted"`
		}
		if json.Unmarshal(bytes.TrimSpace(out), &result) != nil || result.Accepted != len(rows) {
			return fmt.Errorf("Airlock did not acknowledge %d rows: %.500s", len(rows), out)
		}
		if _, err := u.state.WriteString(strings.Join(keys, "\n") + "\n"); err != nil {
			return err
		}
		if err := u.state.Sync(); err != nil {
			return err
		}
		for _, k := range keys {
			u.uploaded[k] = true
		}
		count += len(rows)
		rows = nil
		keys = nil
		size = 0
		return nil
	}
	line, excluded := 0, 0
	for scan.Scan() {
		line++
		row := bytes.TrimSpace(scan.Bytes())
		if len(row) == 0 {
			continue
		}
		digest := sha256.Sum256(row)
		key := hex.EncodeToString(digest[:])
		if u.uploaded[key] {
			continue
		}
		tier, reversed, err := reversedTier(row)
		if err != nil {
			return count, fmt.Errorf("line %d: %w", line, err)
		}
		if reversed {
			if err := quarantineReversedTier(u.state.Name(), path, line, key, scan.Bytes(), tier); err != nil {
				return count, fmt.Errorf("preserve contradictory quantity at line %d: %w", line, err)
			}
			excluded++
			continue
		}
		if len(row) > 90000 {
			return count, errors.New("observation exceeds SDK CLI argument size")
		}
		if size+len(row) > 90000 || len(rows) >= 200 {
			if err := flush(); err != nil {
				return count, err
			}
		}
		rows = append(rows, json.RawMessage(bytes.Clone(row)))
		keys = append(keys, key)
		size += len(row) + 1
	}
	if err := scan.Err(); err != nil {
		return count, err
	}
	if err := flush(); err != nil {
		return count, err
	}
	if excluded > 0 {
		fmt.Fprintf(os.Stderr, "%s: %d rows excluded for contradictory quantity; audit: %s.quarantine\n", path, excluded, u.state.Name())
	}
	return count, nil
}
