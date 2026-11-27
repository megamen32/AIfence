package delivery

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"

	"github.com/airlockrun/agentsdk"
)

// memDB is a tiny database/sql driver that understands exactly the four
// statements this package issues.
type memDB struct {
	mu   sync.Mutex
	rows map[string][]byte
}

var (
	registerOnce sync.Once
	current      *memDB
)

type memDriver struct{}
type memConn struct{ db *memDB }
type memStmt struct {
	db *memDB
	q  string
}
type memRows struct {
	val  []byte
	done bool
}

func (memDriver) Open(string) (driver.Conn, error)      { return memConn{current}, nil }
func (c memConn) Prepare(q string) (driver.Stmt, error) { return memStmt{c.db, q}, nil }
func (memConn) Close() error                            { return nil }
func (memConn) Begin() (driver.Tx, error)               { return nil, errors.New("no tx") }
func (memStmt) Close() error                            { return nil }
func (memStmt) NumInput() int                           { return -1 }
func key(table string, a, b driver.Value) string {
	return table + "|" + a.(string) + "|" + b.(string)
}
func (s memStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	switch {
	case strings.Contains(s.q, "INSERT INTO pm_redmine_issues"):
		k := key("r", args[0], args[1])
		if _, ok := s.db.rows[k]; !ok {
			s.db.rows[k] = args[4].([]byte)
		}
	case strings.Contains(s.q, "INSERT INTO pm_dion_drafts"):
		s.db.rows[key("d", args[0], args[1])] = args[2].([]byte)
	default:
		return nil, errors.New("unexpected exec " + s.q)
	}
	return driver.RowsAffected(1), nil
}
func (s memStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	switch {
	case strings.Contains(s.q, "FROM pm_redmine_issues"):
		return &memRows{val: s.db.rows[key("r", args[0], args[1])]}, nil
	case strings.Contains(s.q, "FROM pm_dion_drafts WHERE owner_id = $1 AND day"):
		return &memRows{val: s.db.rows[key("d", args[0], args[1])]}, nil
	case strings.Contains(s.q, "FROM pm_dion_drafts"):
		var last string
		var val []byte
		for k, v := range s.db.rows {
			if strings.HasPrefix(k, "d|"+args[0].(string)+"|") && k > last {
				last, val = k, v
			}
		}
		return &memRows{val: val}, nil
	}
	return nil, errors.New("unexpected query " + s.q)
}
func (r *memRows) Columns() []string { return []string{"v"} }
func (r *memRows) Close() error      { return nil }
func (r *memRows) Next(dest []driver.Value) error {
	if r.done || r.val == nil {
		return io.EOF
	}
	r.done = true
	dest[0] = r.val
	return nil
}

func newDB(t *testing.T) *sql.DB {
	registerOnce.Do(func() { sql.Register("pmmem", memDriver{}) })
	current = &memDB{rows: map[string][]byte{}}
	db, err := sql.Open("pmmem", "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

type fakeRedmine struct {
	issues   []map[string]any
	creates  int
	notes    int
	failures map[string]int
}

func structured(v any) (*agentsdk.MCPToolCallResponse, error) {
	raw, err := json.Marshal(v)
	return &agentsdk.MCPToolCallResponse{StructuredContent: raw}, err
}

func (f *fakeRedmine) CallTool(_ context.Context, name string, raw any) (*agentsdk.MCPToolCallResponse, error) {
	if f.failures != nil && f.failures[name] > 0 {
		f.failures[name]--
		return nil, errors.New("synthetic Redmine failure")
	}
	args := raw.(map[string]any)
	switch name {
	case "list_redmine_projects":
		return structured(map[string]any{"result": []map[string]any{{"id": 1, "identifier": "personal", "name": "Личные задачи"}}})
	case "list_redmine_trackers":
		return structured(map[string]any{"result": []map[string]any{{"id": 1, "name": "Ошибка"}, {"id": 2, "name": "Улучшение"}}})
	case "list_project_trackers":
		return structured([]map[string]any{{"id": 1, "name": "Ошибка"}, {"id": 2, "name": "Улучшение"}})
	case "list_project_issue_custom_fields":
		return structured(map[string]any{"result": []map[string]any{{"id": 1, "name": FieldAI}, {"id": 2, "name": FieldSignal}}})
	case "list_redmine_issues":
		out := []map[string]any{}
		filters, _ := args["filters"].(map[string]any)
		for _, is := range f.issues {
			fields, _ := is["fields"].(map[string]any)
			if want, ok := filters["cf_2"]; ok {
				if fields[FieldSignal] == want {
					out = append(out, is)
				}
				continue
			}
			if args["status_id"] == "open" {
				if status, _ := is["status_id"].(string); status == "" || status == "open" {
					out = append(out, is)
				}
			}
		}
		return structured(map[string]any{"result": out})
	case "get_redmine_issue":
		id := int(args["issue_id"].(int))
		for _, is := range f.issues {
			if is["id"] == id {
				return structured(is)
			}
		}
		return structured(map[string]any{"id": id})
	case "create_redmine_issue":
		f.creates++
		is := map[string]any{"id": 100 + f.creates, "subject": args["subject"], "description": args["description"], "fields": args["fields"], "status_id": "open"}
		f.issues = append(f.issues, is)
		return structured(map[string]any{"id": is["id"], "subject": is["subject"]})
	case "update_redmine_issue":
		f.notes++
		for _, is := range f.issues {
			if is["id"] == args["issue_id"] {
				journals, _ := is["journals"].([]map[string]any)
				journals = append(journals, map[string]any{"id": len(journals) + 1, "notes": args["fields"].(map[string]any)["notes"]})
				is["journals"] = journals
			}
		}
		return structured(map[string]any{"id": args["issue_id"]})
	}
	return nil, errors.New("unexpected tool " + name)
}

func approved() decision.Record {
	return decision.Record{
		Hypothesis: decision.Hypothesis{ID: "watch-kes-1", Title: "Падение заказов Kaspersky Endpoint Security", ProductKey: "kaspersky-endpoint-security",
			SignalIDs: []string{"sig-kes-2026-11-24"}, SupportIDs: []string{"obs-metrika-17"}, ProposedAction: "Проверить шаг оплаты на карточке KL4863RAKFS",
			SuccessCriterion: decision.Criterion{MetricKey: "family_orders_idx", Rule: "relative_change_pct >= 10", BaselineFrom: "2026-10-27", BaselineTo: "2026-11-23", ObservationDays: 14, ControlProductKey: "astra-linux"}},
		SignalContrast:     &evidence.WindowContrast{Basis: "capped_four_week_relative_change", Baseline: evidence.Period{From: "2026-10-27", To: "2026-11-23"}, Current: evidence.Period{From: "2026-11-24", To: "2026-11-30"}, BaselineMean: 110, CurrentMean: 74.5, ControlBaselineMean: 52, ControlCurrentMean: 51, RelativeChangePct: -32.3},
		EvidenceSnapshotID: "snap-1",
		Task:               &decision.Task{ID: "PM-abc"},
	}
}

func TestRedmineDeliverIsIdempotentPerSignal(t *testing.T) {
	db := newDB(t)
	rm := &fakeRedmine{}
	d := &Redmine{MCP: rm, DB: db, Project: "personal", Tracker: "Улучшение", PublicURL: "http://redmine.localhost:42080"}
	first, err := d.Deliver(context.Background(), "owner", approved())
	if err != nil {
		t.Fatal(err)
	}
	if first.IssueID != 101 || first.URL != "http://redmine.localhost:42080/issues/101" {
		t.Fatalf("unexpected receipt %+v", first)
	}
	again, err := d.Deliver(context.Background(), "owner", approved())
	if err != nil || again.IssueID != first.IssueID || rm.creates != 1 {
		t.Fatalf("second delivery must reuse the issue: %+v creates=%d err=%v", again, rm.creates, err)
	}
	// Lost local receipt: Redmine search by the signal field finds the issue.
	current.rows = map[string][]byte{}
	found, err := d.Deliver(context.Background(), "owner", approved())
	if err != nil || found.IssueID != 101 || !found.Reused || rm.creates != 1 {
		t.Fatalf("lost receipt must not duplicate: %+v creates=%d err=%v", found, rm.creates, err)
	}
	is := rm.issues[0]
	desc := is["description"].(string)
	for _, want := range []string{"110", "74.5", "-32.3%", "sig-kes-2026-11-24", "obs-metrika-17", AILabel, "astra-linux"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description misses %q:\n%s", want, desc)
		}
	}
	fields := is["fields"].(map[string]any)
	if fields["tracker_id"] != 2 || fields[FieldAI] != "1" || fields[FieldSignal] != "sig-kes-2026-11-24" || !strings.HasPrefix(is["subject"].(string), "[ИИ] ") {
		t.Fatalf("wrong fields or subject: %v %v", fields, is["subject"])
	}
}

func TestRedmineCommentsOnExistingTaskForSameProduct(t *testing.T) {
	rm := &fakeRedmine{issues: []map[string]any{{"id": 7, "subject": "Падение заказов Kaspersky Endpoint Security", "description": "*Продукт:* kaspersky-endpoint-security\nручная задача", "fields": map[string]any{}}}}
	d := &Redmine{MCP: rm, DB: newDB(t), Project: "personal", Tracker: "Улучшение", PublicURL: "http://r"}
	got, err := d.Deliver(context.Background(), "owner", approved())
	if err != nil || got.IssueID != 7 || !got.Noted || rm.creates != 0 || rm.notes != 1 {
		t.Fatalf("existing task must get a comment instead of a duplicate: %+v creates=%d notes=%d err=%v", got, rm.creates, rm.notes, err)
	}
	if _, err := d.Deliver(context.Background(), "owner", approved()); err != nil || rm.notes != 1 {
		t.Fatalf("repeat must not comment twice, notes=%d", rm.notes)
	}
}

func TestEligibleNeedsVerifiedWindow(t *testing.T) {
	r := approved()
	if !Eligible(r) {
		t.Fatal("verified signal must be eligible")
	}
	r.SignalContrast = nil
	if Eligible(r) {
		t.Fatal("a card without a verified window must not reach Redmine")
	}
}

type fakeUserIO struct {
	prepared, sent int
	lastSend       map[string]any
}

func (f *fakeUserIO) CallTool(_ context.Context, name string, args any) (*agentsdk.MCPToolCallResponse, error) {
	switch name {
	case "userio.messages.prepare":
		f.prepared++
		body, _ := args.(map[string]any)["text"].(string)
		raw, _ := json.Marshal(map[string]any{"draft_id": "drf_1", "status": "draft", "conversation_id": "conv_21969b7f6d7f3d0d3c3cb7e9", "channel": "dion", "body": body})
		return &agentsdk.MCPToolCallResponse{StructuredContent: raw}, nil
	case "userio.messages.send":
		f.sent++
		f.lastSend = args.(map[string]any)
		return &agentsdk.MCPToolCallResponse{StructuredContent: json.RawMessage(`{"sent":true,"delivery_confirmed":true,"delivery_status":"delivered","receipt":"dion:test-receipt"}`)}, nil
	}
	return nil, errors.New("unexpected tool " + name)
}

func TestDionPrepareThenSendOnlyToTestChat(t *testing.T) {
	chat := "conv_21969b7f6d7f3d0d3c3cb7e9"
	u := &fakeUserIO{}
	d := &Dion{MCP: u, DB: newDB(t), Chat: func(context.Context) (string, error) { return chat, nil }}
	now := time.Date(2026, 11, 28, 4, 0, 0, 0, time.UTC)
	dr, err := d.Prepare(context.Background(), "owner", now, []decision.Record{approved()})
	if err != nil {
		t.Fatal(err)
	}
	if u.sent != 0 || dr.Sent || dr.DraftID != "drf_1" || !strings.Contains(dr.Text, "-32.3%") || !strings.Contains(dr.Text, "sig-kes-2026-11-24") {
		t.Fatalf("prepare must only draft: %+v sent=%d", dr, u.sent)
	}
	if _, err := d.Prepare(context.Background(), "owner", now, nil); err != nil || u.prepared != 1 {
		t.Fatalf("same day must reuse the draft, prepared=%d err=%v", u.prepared, err)
	}
	chat = "conv_other0000000"
	if _, err := d.Send(context.Background(), "owner", dr.Day, now); err == nil || u.sent != 0 {
		t.Fatal("a draft for another chat must not be sent")
	}
	chat = "conv_21969b7f6d7f3d0d3c3cb7e9"
	got, err := d.Send(context.Background(), "owner", dr.Day, now)
	if err != nil || !got.Sent || u.sent != 1 || u.lastSend["expected_chat_id"] != chat || u.lastSend["expected_text"] != dr.Text {
		t.Fatalf("send failed: %+v %v %v", got, err, u.lastSend)
	}
	if _, err := d.Send(context.Background(), "owner", dr.Day, now); err != nil || u.sent != 1 {
		t.Fatalf("repeat confirmation must not resend, sent=%d", u.sent)
	}
}

func TestDionNeedsTestChat(t *testing.T) {
	d := &Dion{MCP: &fakeUserIO{}, DB: newDB(t), Chat: func(context.Context) (string, error) { return "", nil }}
	if _, err := d.Prepare(context.Background(), "owner", time.Now(), nil); err == nil {
		t.Fatal("empty test chat must disable Dion")
	}
}
