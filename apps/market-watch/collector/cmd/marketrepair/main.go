package main

import (
	collector "agent/collector"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofrs/flock"
)

func ask(ctx context.Context, air, appDir string, b collector.RepairBatch) (collector.RuleProposal, error) {
	raw, e := json.Marshal(b)
	if e != nil {
		return collector.RuleProposal{}, e
	}
	if len(raw) > 180000 {
		return collector.RuleProposal{}, fmt.Errorf("batch exceeds safe CLI input limit")
	}
	modelCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(modelCtx, air, "tools", "invoke", "propose_parser_repair", "--dir", appDir, "--input", string(raw), "--timeout", "2m", "--json")
	out, e := cmd.Output()
	if e != nil {
		return collector.RuleProposal{}, fmt.Errorf("native repair model unavailable (%T)", e)
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Result struct {
				Output json.RawMessage `json:"output"`
			} `json:"result"`
		} `json:"data"`
	}
	if e = json.Unmarshal(out, &envelope); e != nil {
		return collector.RuleProposal{}, fmt.Errorf("invalid SDK repair response")
	}
	if envelope.Status != "completed" {
		return collector.RuleProposal{}, fmt.Errorf("native repair call not completed")
	}
	value := envelope.Data.Result.Output
	var text string
	if json.Unmarshal(value, &text) == nil {
		value = []byte(text)
	}
	return collector.DecodeProposal(bytes.NewReader(value))
}
func once(ctx context.Context, j *collector.Journal, root, air, app string) error {
	// Deterministic code owns classification and requeues. The LLM only
	// proposes a new selector after the existing parser has been tried.
	audit, e := j.PreflightRepairs(256)
	if e != nil {
		return e
	}
	if audit.Checked > 0 {
		fmt.Printf("code audit checked=%d requeued=%d states=%v\n", audit.Checked, audit.Requeued, audit.States)
	}
	if audit.Checked == 256 {
		return nil
	} // finish the existing code backlog first

	b, e := j.NextRepairBatch(time.Now())
	if e != nil || b == nil {
		return e
	}
	dir := filepath.Join(root, "repair", "batches")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	raw, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, b.ID+".json"), raw, 0600); e != nil {
		return e
	}
	if e = j.MarkBatch(*b, "waiting_model", "requesting configured native model"); e != nil {
		return e
	}
	proposal, e := ask(ctx, air, app, *b)
	if e != nil {
		_ = j.MarkBatch(*b, "waiting_model", e.Error())
		return e
	}
	if proposal.NoChange {
		// An abstention supplies no rule to test. It is NOT a verdict on the
		// product or a failure resolution. Retain the model's exact reason.
		receipt, e := json.Marshal(struct {
			Proposal collector.RuleProposal `json:"proposal"`
			Decision string                 `json:"decision"`
		}{proposal, "no candidate supplied; tasks remain unresolved"})
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dir, b.ID+".report.json"), receipt, 0600); e != nil {
			return e
		}
		return j.MarkBatch(*b, "no_candidate", string(receipt))
	}
	report, e := j.ValidateRepair(*b, proposal)
	if e != nil {
		return e
	}
	receipt, e := json.MarshalIndent(struct {
		Proposal collector.RuleProposal `json:"proposal"`
		Report   collector.RepairReport `json:"report"`
	}{proposal, report}, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, b.ID+".report.json"), receipt, 0600); e != nil {
		return e
	}
	if !report.Accepted {
		return j.MarkBatch(*b, "rejected", string(receipt))
	}
	if e = j.ActivateRepair(*b, proposal, report); e != nil {
		return e
	}
	fmt.Printf("repair accepted source=%s examples=%d corpus=%d improved=%d held_out=%d regressions=%d\n", b.Source, len(b.Examples), report.Evaluated, report.Improvements, report.HeldOutImprovements, report.Regressions)
	return nil
}
func run() error {
	root := flag.String("out", "", "catalog root")
	air := flag.String("air", "", "existing Airlock CLI path")
	app := flag.String("app-dir", "", "existing bound Market Watch app directory")
	single := flag.Bool("once", false, "run one repair check")
	interval := flag.Duration("interval", time.Minute, "repair scan interval")
	flag.Parse()
	if *root == "" || *air == "" || *app == "" || *interval < time.Minute {
		return fmt.Errorf("out, air, app-dir and interval >= 1m are required")
	}
	lock := flock.New(filepath.Join(*root, "repair.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("another repair process is active")
	}
	defer lock.Unlock()
	j, e := collector.OpenJournal(*root)
	if e != nil {
		return e
	}
	defer j.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		if e = once(ctx, j, *root, *air, *app); e != nil && !errors.Is(e, context.Canceled) {
			fmt.Fprintln(os.Stderr, e)
		}
		if *single {
			return e
		}
		t := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
