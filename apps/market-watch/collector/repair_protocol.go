package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const RepairMinExamples = 10
const RepairMaxExamples = 20

type RepairExample struct {
	EvidenceID int64     `json:"evidence_id"`
	URL        string    `json:"url"`
	CapturedAt time.Time `json:"captured_at"`
	HTML       string    `json:"html_excerpt"`
}
type RepairBatch struct {
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	ErrorType   string          `json:"error_type"`
	CurrentRule Rule            `json:"current_rule"`
	ActiveRules []RuleProposal  `json:"active_rules"`
	Examples    []RepairExample `json:"examples"`
}

// Declarative rules only: the model never executes generated Go, shell or JS.
// An exact canonical product, explicit currency, SKU and two agreeing price
// fields are required. The model cannot set a numeric constant.
type RuleProposal struct {
	NoChange     bool   `json:"no_change"`
	Reason       string `json:"reason"`
	Source       string `json:"source"`
	Container    string `json:"container"`
	Name         Field  `json:"name"`
	SKU          Field  `json:"sku"`
	Price        Field  `json:"price"`
	ConfirmPrice Field  `json:"confirm_price"`
	Currency     Field  `json:"currency"`
}

// Strict JSON only. Trailing markdown/code and unknown fields are rejected.
func DecodeProposal(r io.Reader) (RuleProposal, error) {
	var out RuleProposal
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if e := d.Decode(&out); e != nil {
		return out, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return out, fmt.Errorf("expected exactly one JSON object")
	}
	return out, nil
}
