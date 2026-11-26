package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"agent/internal/db"
	"agent/internal/decision"
	"agent/internal/evidence"

	"github.com/airlockrun/agentsdk"
)

type SignalNotifications struct {
	Enabled *agentsdk.EnvVarHandle
	Topic   *agentsdk.TopicHandle
	DB      *agentsdk.AgentDB
}

func (n *SignalNotifications) publish(ctx context.Context, r decision.Record, rows []evidence.Row) {
	if n == nil {
		return
	}
	mode, err := n.Enabled.Get(ctx)
	if err != nil || mode != "bridge" {
		return
	}
	user, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok {
		return
	}
	base, err := agentsdk.AgentURLFromContext(ctx)
	if err != nil {
		return
	}
	page := strings.TrimRight(base, "/") + "/?id=" + url.QueryEscape(r.Hypothesis.ID)
	key := notificationKey(r.Hypothesis, rows)
	if key == "" {
		return
	}
	claimed, err := db.New(n.DB).PMClaimSignalNotification(ctx, db.PMClaimSignalNotificationParams{OwnerID: user.ID, SignalKey: key})
	if err != nil {
		slog.WarnContext(ctx, "Product Manager: could not reserve signal notification")
		return
	}
	if claimed == 0 {
		return
	}
	// A timed-out publish may already have delivered to some routes.
	if err := n.Topic.PublishToUser(ctx, user.ID, []agentsdk.DisplayPart{{Type: "text", Text: "Новый продуктовый сигнал. Открыть карточку: " + page}}); err != nil {
		slog.WarnContext(ctx, "Product Manager: signal notification outcome unconfirmed")
	}
}

func notificationKey(h decision.Hypothesis, rows []evidence.Row) string {
	signals := slices.Clone(h.SignalIDs)
	slices.Sort(signals)
	signals = slices.Compact(signals)
	observed := false
	for _, row := range rows {
		if !slices.Contains(signals, row.ID) {
			continue
		}
		var provenance struct {
			Origin    string `json:"origin"`
			Synthetic bool   `json:"synthetic"`
		}
		_ = json.Unmarshal([]byte(row.Details), &provenance)
		if strings.HasPrefix(row.Ref.SourceID, "synthetic/") || provenance.Origin == "synthetic_fixture" || provenance.Synthetic {
			return ""
		}
		observed = observed || row.Status == "observed"
	}
	if !observed {
		return ""
	}
	body, _ := json.Marshal(append([]string{h.ProductKey}, signals...))
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
