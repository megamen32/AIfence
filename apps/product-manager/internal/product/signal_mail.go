package product

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"agent/internal/db"
	"agent/internal/decision"
	"agent/internal/evidence"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

type SignalMail struct {
	Config *agentsdk.EnvVarHandle
	DB     *agentsdk.AgentDB
}

type smtpSettings struct {
	OwnerID  string `json:"owner_id"`
	Address  string `json:"address"`
	From     string `json:"from"`
	To       string `json:"to"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLSMode  string `json:"tls_mode"`
}

func parseSMTP(raw string) (smtpSettings, error) {
	var c smtpSettings
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, errors.New("invalid SMTP configuration")
	}
	if owner, err := uuid.Parse(c.OwnerID); err != nil || owner == uuid.Nil || owner.String() != c.OwnerID {
		return c, errors.New("SMTP owner_id must be a canonical Airlock user UUID")
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || host == "" || port == "" || (c.Username == "") != (c.Password == "") {
		return c, errors.New("invalid SMTP address or credentials")
	}
	for _, mailbox := range []string{c.From, c.To} {
		address, err := mail.ParseAddress(mailbox)
		if err != nil || address.Address != mailbox || strings.ContainsAny(mailbox, "\r\n") {
			return c, errors.New("SMTP requires a single bare address for each of from and to")
		}
	}
	if c.TLSMode == "" {
		c.TLSMode = "starttls"
	}
	if c.TLSMode != "starttls" && c.TLSMode != "implicit" {
		return c, errors.New("SMTP requires STARTTLS or implicit TLS")
	}
	return c, nil
}

func realSignalID(draft decision.SaveInput, rows []evidence.Row) string {
	if len(draft.Hypothesis.SignalIDs) != 1 {
		return ""
	}
	id := draft.Hypothesis.SignalIDs[0]
	for _, r := range rows {
		if r.ID != id || r.Product != draft.Hypothesis.ProductKey || r.Source != "sales" ||
			r.Kind != "metric" || r.Metric != WatchdogMetric || r.Status != "observed" || r.Value != "true" ||
			r.Ref.SourceID != "aifence-watchdog" || !strings.HasPrefix(id, "watchdog/") {
			continue
		}
		var provenance struct {
			Origin    string `json:"origin"`
			Synthetic bool   `json:"synthetic"`
		}
		if json.Unmarshal([]byte(r.Details), &provenance) == nil && !provenance.Synthetic &&
			(provenance.Origin == "imported_historical" || provenance.Origin == "live_api") {
			return id
		}
	}
	return ""
}

func signalCardURL(base, id string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return "", errors.New("invalid app URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip.IsLoopback())) {
		return "", errors.New("app URL must use HTTPS or loopback HTTP")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/") + "/"
	}
	u.RawQuery = url.Values{"id": {id}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func (m *SignalMail) publish(ctx context.Context, r decision.Record, draft decision.SaveInput, rows []evidence.Row) {
	if m == nil {
		return
	}
	signal := realSignalID(draft, rows)
	if signal == "" {
		return
	}
	settings, err := m.Config.Get(ctx)
	if err != nil || settings == "" {
		return
	}
	cfg, err := parseSMTP(settings)
	if err != nil {
		slog.WarnContext(ctx, "Product Manager: invalid signal SMTP settings")
		return
	}
	user, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok || user.ID != cfg.OwnerID {
		return
	}
	base, err := agentsdk.AgentURLFromContext(ctx)
	if err != nil {
		return
	}
	link, err := signalCardURL(base, r.Hypothesis.ID)
	if err != nil {
		slog.WarnContext(ctx, "Product Manager: signal email requires a valid app URL")
		return
	}
	q := db.New(m.DB)
	key := "smtp:" + signal
	err = deliverSignalMail(ctx, func(ctx context.Context) (bool, error) {
		count, err := q.PMClaimSignalNotification(ctx, db.PMClaimSignalNotificationParams{OwnerID: user.ID, SignalKey: key})
		return count == 1, err
	}, func(ctx context.Context) error {
		return q.PMReleaseSignalNotification(ctx, db.PMReleaseSignalNotificationParams{OwnerID: user.ID, SignalKey: key})
	}, func(ctx context.Context) (bool, error) {
		return cfg.send(ctx, link, nil)
	})
	if err != nil {
		slog.WarnContext(ctx, "Product Manager: signal email delivery not confirmed")
	}
}

// The claim survives delivery and uncertain delivery. SMTP may accept a message
// even if the client never receives its final reply.
func deliverSignalMail(ctx context.Context, claim func(context.Context) (bool, error), release func(context.Context) error, send func(context.Context) (bool, error)) error {
	claimed, err := claim(ctx)
	if err != nil || !claimed {
		return err
	}
	started, err := send(ctx)
	if err != nil && !started {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		return errors.Join(err, release(cleanup))
	}
	return err
}

func (cfg smtpSettings) send(ctx context.Context, link string, roots *x509.CertPool) (bool, error) {
	host, _, _ := net.SplitHostPort(cfg.Address)
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: roots}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var transport net.Conn = conn
	if cfg.TLSMode == "implicit" {
		transport = tls.Client(conn, tlsConfig)
	}
	c, err := smtp.NewClient(transport, host)
	if err != nil {
		return false, err
	}
	defer c.Close()
	if cfg.TLSMode == "starttls" {
		if err := c.StartTLS(tlsConfig); err != nil {
			return false, err
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
			return false, err
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return false, err
	}
	if err := c.Rcpt(cfg.To); err != nil {
		return false, err
	}
	w, err := c.Data()
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: Product Manager: new signal\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nOpen the decision card in Airlock:\r\n%s\r\n", cfg.From, cfg.To, time.Now().UTC().Format(time.RFC1123Z), link)
	if err != nil {
		return true, err
	}
	return true, w.Close()
}
