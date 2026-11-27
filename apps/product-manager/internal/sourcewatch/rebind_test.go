package sourcewatch

import (
	"agent/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

const rebindOwner = "00000000-0000-0000-0000-000000000001"
const replacementBinding = "55555555-5555-4555-8555-555555555555"

type bindingFixture struct {
	rows         map[string]db.PmSourceWatch
	gets, writes int
	beforeWrite  func()
}

func watchKey(owner, watch string) string { return owner + "/" + watch }

func (r *bindingFixture) GetSourceWatch(_ context.Context, in db.GetSourceWatchParams) (db.PmSourceWatch, error) {
	r.gets++
	row, ok := r.rows[watchKey(in.OwnerID, in.WatchID)]
	if !ok {
		return db.PmSourceWatch{}, sql.ErrNoRows
	}
	row.Config = append(json.RawMessage(nil), row.Config...)
	return row, nil
}

func (r *bindingFixture) RebindSourceWatch(_ context.Context, in db.RebindSourceWatchParams) (db.PmSourceWatch, error) {
	r.writes++
	if r.beforeWrite != nil {
		r.beforeWrite()
		r.beforeWrite = nil
	}
	key := watchKey(in.OwnerID, in.WatchID)
	row, ok := r.rows[key]
	if !ok || string(row.Config) != string(in.ExpectedConfig) {
		return db.PmSourceWatch{}, sql.ErrNoRows
	}
	var config, patch map[string]json.RawMessage
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return db.PmSourceWatch{}, err
	}
	if err := json.Unmarshal(in.BindingPatch, &patch); err != nil {
		return db.PmSourceWatch{}, err
	}
	for key, value := range patch {
		switch key {
		case "metrika_binding", "market_binding", "wordstat_binding", "trends_binding":
			config[key] = value
		default:
			return db.PmSourceWatch{}, errors.New("attempt to change non-binding configuration")
		}
	}
	row.Config, _ = json.Marshal(config)
	r.rows[key] = row
	return row, nil
}

func bindingCaller(t *testing.T, owner string, access wire.Access) context.Context {
	t.Helper()
	u := wire.CallerUser{ID: owner, PlatformMember: true}
	return testutil.WithCaller(t.Context(), wire.Caller{
		Kind: "user", Access: access, User: &u, Initiator: &u,
		Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"},
	})
}

func watchFixture(t *testing.T) (*bindingFixture, db.PmSourceWatch) {
	t.Helper()
	w := allSourcesWindow()
	w.SKU, w.SKUs = "", []string{"KL4141RCEFS", "KL4861RAEFS"}
	w.ProductKey, w.Segment = "kaspersky_endpoint_security", "retail"
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	// Unknown future config is preserved exactly, including integers above float precision.
	document["future_option"] = json.RawMessage(`{"precision":9007199254740993}`)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	row := db.PmSourceWatch{
		OwnerID: rebindOwner, WatchID: ID(rebindOwner, "watch", w), Config: raw,
		Active: true, LastMonday: "2026-11-23", LastStatus: "submitted", AnalysisRunID: "existing-review",
	}
	return &bindingFixture{rows: map[string]db.PmSourceWatch{watchKey(rebindOwner, row.WatchID): row}}, row
}

func TestRebindPreservesWatchConfigurationHistoryAndQueuedIdentity(t *testing.T) {
	repo, before := watchFixture(t)
	ctx := bindingCaller(t, rebindOwner, wire.AccessUser)
	queued := JobInput{WatchID: before.WatchID, Monday: "2026-11-30"}
	queuedID := ID(rebindOwner, "delivery", queued)
	var original map[string]json.RawMessage
	if err := json.Unmarshal(before.Config, &original); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		out, err := rebind(ctx, repo, RebindInput{WatchID: before.WatchID, MarketBinding: replacementBinding})
		if err != nil {
			t.Fatal(err)
		}
		if out.ID != before.WatchID || !out.Active || out.LastMonday != before.LastMonday || out.Status != before.LastStatus || out.AnalysisRunID != before.AnalysisRunID {
			t.Fatalf("rebind changed watch history: %+v", out)
		}
		if out.Config.MarketBinding != replacementBinding {
			t.Fatalf("binding was not replaced: %+v", out.Config)
		}
		if ID(rebindOwner, "delivery", JobInput{WatchID: out.ID, Monday: queued.Monday}) != queuedID {
			t.Fatal("rebind invalidated the already queued delivery")
		}
		after := repo.rows[watchKey(rebindOwner, before.WatchID)]
		var config map[string]json.RawMessage
		if err := json.Unmarshal(after.Config, &config); err != nil {
			t.Fatal(err)
		}
		delete(config, "market_binding")
		want := make(map[string]json.RawMessage, len(original))
		for k, v := range original {
			if k != "market_binding" {
				want[k] = v
			}
		}
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("rebind modified non-binding configuration: got %s", after.Config)
		}
		if len(repo.rows) != 1 {
			t.Fatal("rebind created another watch")
		}
	}
}

func TestRebindOwnerIsolationAndHumanAuthority(t *testing.T) {
	repo, row := watchFixture(t)
	other := bindingCaller(t, "00000000-0000-0000-0000-000000000002", wire.AccessAdmin)
	if _, err := rebind(other, repo, RebindInput{WatchID: row.WatchID, MarketBinding: replacementBinding}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("another owner could inspect or rebind the watch: %v", err)
	}
	if repo.writes != 0 {
		t.Fatal("foreign owner reached the mutation")
	}
	for _, ctx := range []context.Context{
		bindingCaller(t, rebindOwner, wire.AccessPublic),
		testutil.WithCaller(t.Context(), wire.Caller{Kind: "application", Access: wire.AccessAdmin, Origin: wire.CallerOrigin{Interface: "application", Execution: "background"}}),
		testutil.WithCaller(t.Context(), wire.Caller{Kind: "anonymous", Access: wire.AccessPublic, Origin: wire.CallerOrigin{Interface: "http", Execution: "request"}}),
	} {
		if _, err := rebind(ctx, nil, RebindInput{WatchID: row.WatchID, MarketBinding: replacementBinding}); err == nil {
			t.Fatal("non-owner authority reached watch storage")
		}
	}
	if string(repo.rows[watchKey(rebindOwner, row.WatchID)].Config) != string(row.Config) {
		t.Fatal("owner isolation changed the configuration")
	}
}

func TestRebindDoesNotOverwriteConcurrentConfig(t *testing.T) {
	repo, row := watchFixture(t)
	repo.beforeWrite = func() {
		current := repo.rows[watchKey(rebindOwner, row.WatchID)]
		var config map[string]json.RawMessage
		if err := json.Unmarshal(current.Config, &config); err != nil {
			t.Fatal(err)
		}
		config["metric"] = json.RawMessage(`"orders"`)
		current.Config, _ = json.Marshal(config)
		repo.rows[watchKey(rebindOwner, row.WatchID)] = current
	}
	_, err := rebind(bindingCaller(t, rebindOwner, wire.AccessUser), repo, RebindInput{WatchID: row.WatchID, MarketBinding: replacementBinding})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("concurrent update was overwritten: %v", err)
	}
	var current Window
	if err := json.Unmarshal(repo.rows[watchKey(rebindOwner, row.WatchID)].Config, &current); err != nil {
		t.Fatal(err)
	}
	if current.Metric != "orders" || current.MarketBinding != marketBinding {
		t.Fatalf("rebind changed a competing update: %+v", current)
	}
}

func TestRebindRejectsInvalidBindingChanges(t *testing.T) {
	for _, in := range []RebindInput{
		{},
		{MarketBinding: "not-a-binding"},
		{MarketBinding: metBinding},
	} {
		repo, row := watchFixture(t)
		in.WatchID = row.WatchID
		if _, err := rebind(bindingCaller(t, rebindOwner, wire.AccessUser), repo, in); err == nil {
			t.Fatalf("invalid binding accepted: %+v", in)
		}
		if repo.writes != 0 {
			t.Fatal("invalid replacement reached storage")
		}
	}
	repo, row := watchFixture(t)
	var cfg Window
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.WordstatBinding = ""
	row.Config, _ = json.Marshal(cfg)
	repo.rows[watchKey(rebindOwner, row.WatchID)] = row
	_, err := rebind(bindingCaller(t, rebindOwner, wire.AccessUser), repo, RebindInput{WatchID: row.WatchID, WordstatBinding: replacementBinding})
	if err == nil || repo.writes != 0 {
		t.Fatal("rebind must not add an unselected source")
	}
}
