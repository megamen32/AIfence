// Package decision implements the user-scoped product decision lifecycle.
package decision

type Criterion struct {
	MetricKey         string `json:"metric_key"`
	BaselineFrom      string `json:"baseline_from"`
	BaselineTo        string `json:"baseline_to"`
	ObservationDays   int    `json:"observation_days"`
	Rule              string `json:"rule"`
	ControlProductKey string `json:"control_product_key,omitempty"`
}
type EconomicEffect struct {
	Status      string   `json:"status"`
	Amount      *float64 `json:"amount,omitempty"`
	Currency    string   `json:"currency,omitempty"`
	Assumptions []string `json:"assumptions"`
}
type BacklogRef struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	Tag string `json:"tag"`
}
type Hypothesis struct {
	SchemaVersion         string         `json:"schema_version" jsonschema:"description=Set this field to the exact literal aifence.v1"`
	ID                    string         `json:"id"`
	Title                 string         `json:"title"`
	ProductKey            string         `json:"product_key"`
	SignalIDs             []string       `json:"signal_ids"`
	SupportIDs            []string       `json:"support_ids"`
	CounterIDs            []string       `json:"counter_ids"`
	EvidenceLevel         string         `json:"evidence_level,omitempty" jsonschema:"enum=signal_only,enum=correlation,enum=path_verified"`
	Forecast              string         `json:"forecast,omitempty"`
	CompetingExplanations []string       `json:"competing_explanations,omitempty"`
	BrowserEvidenceIDs    []string       `json:"browser_evidence_ids,omitempty"`
	Unknowns              []string       `json:"unknowns"`
	ProposedAction        string         `json:"proposed_action"`
	SuccessCriterion      Criterion      `json:"success_criterion"`
	EconomicEffect        EconomicEffect `json:"economic_effect"`
	Status                string         `json:"status"`
	BacklogRef            *BacklogRef    `json:"backlog_ref,omitempty"`
	ReleaseID             string         `json:"release_id,omitempty"`
	DecisionReason        string         `json:"decision_reason,omitempty"`
}
type SaveInput struct {
	ContextIDs         []string   `json:"context_ids,omitempty"`
	Hypothesis         Hypothesis `json:"hypothesis"`
	ExpectedVersion    int64      `json:"expected_version"`
	EvidenceSnapshotID string     `json:"evidence_snapshot_id"`
}
type GetInput struct {
	HypothesisID string `json:"hypothesis_id"`
}
type SendInput struct {
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
	HypothesisID    string `json:"hypothesis_id"`
	Confirmed       bool   `json:"confirmed"`
	IdempotencyKey  string `json:"idempotency_key"`
}
type Task struct {
	ID                 string    `json:"id"`
	URL                string    `json:"url"`
	Tag                string    `json:"tag"`
	HypothesisID       string    `json:"hypothesis_id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Tracker            string    `json:"tracker"`
	EvidenceSnapshotID string    `json:"evidence_snapshot_id"`
	Criterion          Criterion `json:"success_criterion"`
}
type PeriodValue struct {
	ProductKey string   `json:"product_key"`
	MetricKey  string   `json:"metric_key"`
	Unit       string   `json:"unit"`
	Segment    string   `json:"segment,omitempty"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	SourceRef  string   `json:"source_ref"`
	Value      *float64 `json:"value"`
	Complete   bool     `json:"complete"`
}
type Measurement struct {
	Quality         string       `json:"quality"`
	Kind            string       `json:"kind"`
	Baseline        PeriodValue  `json:"baseline"`
	Current         PeriodValue  `json:"current"`
	ControlBaseline *PeriodValue `json:"control_baseline,omitempty"`
	ControlCurrent  *PeriodValue `json:"control_current,omitempty"`
}
type EvaluateInput struct {
	HypothesisID       string       `json:"hypothesis_id"`
	ReleaseID          string       `json:"release_id,omitempty"`
	ReleasedOn         string       `json:"released_on,omitempty"`
	ReleaseEvidenceRef string       `json:"release_evidence_ref,omitempty"`
	Measurement        *Measurement `json:"measurement,omitempty"`
}
type Assessment struct {
	Status             string       `json:"status"`
	Reason             string       `json:"reason"`
	ReleaseID          string       `json:"release_id,omitempty"`
	ReleasedOn         string       `json:"released_on,omitempty"`
	ReleaseEvidenceRef string       `json:"release_evidence_ref,omitempty"`
	Value              *float64     `json:"value,omitempty"`
	Unit               string       `json:"unit,omitempty"`
	EvidenceRefs       []string     `json:"evidence_refs"`
	Measurement        *Measurement `json:"measurement,omitempty"`
}
type Record struct {
	ContextIDs         []string    `json:"context_ids,omitempty"`
	Hypothesis         Hypothesis  `json:"hypothesis"`
	Version            int64       `json:"version"`
	EvidenceSnapshotID string      `json:"evidence_snapshot_id"`
	Task               *Task       `json:"task,omitempty"`
	Assessment         *Assessment `json:"assessment,omitempty"`
}
type ListResult struct {
	Records []Record `json:"records"`
}
