package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"agent/internal/decision"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

// MCPCaller is the part of an Airlock MCP handle the Dion delivery needs.
type MCPCaller interface {
	CallTool(ctx context.Context, toolName string, args any) (*agentsdk.MCPToolCallResponse, error)
}

// DionDraft is one prepared morning message. UserIO stores the draft and its
// durable send receipt.
type DionDraft struct {
	Day        string `json:"day"`
	ChatID     string `json:"chat_id"`
	DraftID    string `json:"draft_id"`
	Text       string `json:"text"`
	SignalID   string `json:"signal_id,omitempty"`
	CardURL    string `json:"card_url,omitempty"`
	PreparedAt string `json:"prepared_at"`
	Sent       bool   `json:"sent"`
	SentAt     string `json:"sent_at,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
}

// Dion prepares and sends the morning top-deviation message to one test chat.
type Dion struct {
	MCP  MCPCaller
	DB   agentsdk.DBTX
	Chat func(context.Context) (string, error) // the only allowed UserIO conversation ID
}

var OfficeTime = time.FixedZone("Asia/Yekaterinburg", 5*60*60)

// Top picks the record with the largest verified relative change.
func Top(records []decision.Record) (decision.Record, bool) {
	best, ok := decision.Record{}, false
	for _, r := range records {
		c := r.SignalContrast
		if !Eligible(r) {
			continue
		}
		if !ok || math.Abs(c.RelativeChangePct) > math.Abs(best.SignalContrast.RelativeChangePct) {
			best, ok = r, true
		}
	}
	return best, ok
}

// MorningText renders the message. Numbers come from the verified contrast.
func MorningText(r decision.Record, ok bool, day string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Доброе утро. Сводка Product Manager на %s.\n", day)
	if !ok {
		b.WriteString("Проверенных отклонений нет: ни один сигнал не прошёл порог. Новых задач не предлагаю.")
		return b.String()
	}
	c, h := r.SignalContrast, r.Hypothesis
	title := strings.TrimSpace(h.Title)
	if title == "" {
		title = h.ProductKey
	}
	fmt.Fprintf(&b, "Главное отклонение: %s.\n", title)
	fmt.Fprintf(&b, "%s%% (было %s, стало %s; %s – %s против %s – %s).\n", signed(c.RelativeChangePct), num(c.BaselineMean), num(c.CurrentMean), c.Current.From, c.Current.To, c.Baseline.From, c.Baseline.To)
	if h.SuccessCriterion.ControlProductKey != "" && (c.ControlBaselineMean != 0 || c.ControlCurrentMean != 0) {
		fmt.Fprintf(&b, "Контроль %s: %s → %s.\n", h.SuccessCriterion.ControlProductKey, num(c.ControlBaselineMean), num(c.ControlCurrentMean))
	}
	if a := strings.TrimSpace(h.ProposedAction); a != "" {
		fmt.Fprintf(&b, "Предлагаю: %s\n", a)
	}
	fmt.Fprintf(&b, "Сигнал: %s.", SignalKey(r))
	if r.CardURL != "" {
		fmt.Fprintf(&b, "\nКарточка: %s", r.CardURL)
	}
	return b.String()
}

// Latest returns the owner's most recent morning draft.
func (d *Dion) Latest(ctx context.Context, owner string) (*DionDraft, error) {
	var raw []byte
	err := d.DB.QueryRowContext(ctx, `SELECT document FROM pm_dion_drafts WHERE owner_id = $1 ORDER BY day DESC LIMIT 1`, owner).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out DionDraft
	return &out, json.Unmarshal(raw, &out)
}

func (d *Dion) byDay(ctx context.Context, owner, day string) (*DionDraft, error) {
	var raw []byte
	err := d.DB.QueryRowContext(ctx, `SELECT document FROM pm_dion_drafts WHERE owner_id = $1 AND day = $2`, owner, day).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out DionDraft
	return &out, json.Unmarshal(raw, &out)
}

func (d *Dion) save(ctx context.Context, owner string, dr DionDraft) error {
	raw, err := json.Marshal(dr)
	if err != nil {
		return err
	}
	_, err = d.DB.ExecContext(ctx, `INSERT INTO pm_dion_drafts (owner_id, day, document) VALUES ($1,$2,$3) ON CONFLICT (owner_id, day) DO UPDATE SET document = EXCLUDED.document`, owner, dr.Day, raw)
	return err
}

// Prepare creates today's UserIO draft once per owner and day. An already
// sent day is never re-prepared. Nothing is sent here.
func (d *Dion) Prepare(ctx context.Context, owner string, now time.Time, records []decision.Record) (DionDraft, error) {
	chat, err := d.Chat(ctx)
	if err != nil {
		return DionDraft{}, err
	}
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return DionDraft{}, errors.New("тестовый чат Dion не задан в настройке dion_test_chat")
	}
	day := now.In(OfficeTime).Format("2006-01-02")
	if prev, err := d.byDay(ctx, owner, day); err != nil || (prev != nil && (prev.Sent || prev.ChatID == chat)) {
		if prev != nil {
			return *prev, nil
		}
		return DionDraft{}, err
	}
	top, ok := Top(records)
	text := MorningText(top, ok, day)
	request := uuid.NewSHA1(uuid.NameSpaceURL, []byte("aifence/dion/"+owner+"/"+day+"/"+chat)).String()
	res, err := d.MCP.CallTool(ctx, "userio.messages.prepare", map[string]any{
		"conversation_id": chat, "text": text, "request_id": request,
	})
	if err != nil {
		return DionDraft{}, fmt.Errorf("UserIO: черновик не создан: %w", err)
	}
	var out struct {
		DraftID string `json:"draft_id"`
		ChatID  string `json:"conversation_id"`
		Channel string `json:"channel"`
		Body    string `json:"body"`
		Draft   struct {
			ID string `json:"id"`
		} `json:"draft"`
	}
	if err := decodeMCP(res, &out); err != nil {
		return DionDraft{}, err
	}
	id := out.DraftID
	if id == "" {
		id = out.Draft.ID
	}
	if id == "" {
		return DionDraft{}, errors.New("UserIO не вернул draft_id")
	}
	if out.ChatID != chat || out.Channel != "dion" || out.Body == "" {
		return DionDraft{}, errors.New("UserIO вернул неподходящий черновик Dion")
	}
	text = out.Body
	dr := DionDraft{Day: day, ChatID: chat, DraftID: id, Text: text, PreparedAt: now.UTC().Format(time.RFC3339)}
	if ok {
		dr.SignalID, dr.CardURL = SignalKey(top), top.CardURL
	}
	return dr, d.save(ctx, owner, dr)
}

// Send submits the stored draft with its exact snapshot. Repeated calls return
// the first receipt and never send twice.
func (d *Dion) Send(ctx context.Context, owner, day string, now time.Time) (DionDraft, error) {
	dr, err := d.byDay(ctx, owner, day)
	if err != nil {
		return DionDraft{}, err
	}
	if dr == nil {
		return DionDraft{}, errors.New("черновик на этот день не подготовлен")
	}
	if dr.Sent {
		return *dr, nil
	}
	chat, err := d.Chat(ctx)
	if err != nil {
		return DionDraft{}, err
	}
	if strings.TrimSpace(chat) == "" || dr.ChatID != strings.TrimSpace(chat) {
		return DionDraft{}, errors.New("черновик адресован не тестовому чату, отправка запрещена")
	}
	res, err := d.MCP.CallTool(ctx, "userio.messages.send", map[string]any{
		"draft_id": dr.DraftID, "confirm": true, "expected_text": dr.Text,
		"expected_chat_id": dr.ChatID, "expected_attachments": []string{},
	})
	if err != nil {
		return DionDraft{}, fmt.Errorf("UserIO: ошибка доставки: %w", err)
	}
	var out struct {
		Sent      bool   `json:"sent"`
		Confirmed bool   `json:"delivery_confirmed"`
		Status    string `json:"delivery_status"`
		Receipt   string `json:"receipt"`
	}
	if err := decodeMCP(res, &out); err != nil {
		return DionDraft{}, err
	}
	if !out.Sent || !out.Confirmed || out.Status != "delivered" || out.Receipt == "" {
		return *dr, errors.New("UserIO не подтвердил доставку в Dion")
	}
	dr.Sent, dr.SentAt, dr.Receipt = true, now.UTC().Format(time.RFC3339), out.Receipt
	return *dr, d.save(ctx, owner, *dr)
}

func decodeMCP(res *agentsdk.MCPToolCallResponse, out any) error {
	if res == nil {
		return errors.New("UserIO: пустой ответ")
	}
	var text string
	for _, c := range res.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	if res.IsError {
		if len(text) > 300 {
			text = text[:300]
		}
		return fmt.Errorf("UserIO: %s", text)
	}
	if len(res.StructuredContent) > 0 {
		return json.Unmarshal(res.StructuredContent, out)
	}
	if text != "" && json.Valid([]byte(text)) {
		return json.Unmarshal([]byte(text), out)
	}
	return errors.New("UserIO не вернул JSON")
}
