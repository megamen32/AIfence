package product

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
)

func TestSignalMailSMTPReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverTLS, roots := smtpTestTLS(t)
	type received struct {
		body string
		err  error
	}
	inbox := make(chan received, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
			body, err := receiveSMTP(conn, serverTLS)
			_ = conn.Close()
			inbox <- received{body, err}
		}
	}()
	cfg, err := parseSMTP(`{"owner_id":"00000000-0000-0000-0000-000000000001","address":"` + listener.Addr().String() + `","from":"pm@relay.test","to":"qa@relay.test"}`)
	if err != nil {
		t.Fatal(err)
	}
	link, err := signalCardURL("https://airlock.test/apps/product-manager?session=discarded#fragment", "watch/a+b")
	if err != nil {
		t.Fatal(err)
	}

	draft := decision.SaveInput{Hypothesis: decision.Hypothesis{ProductKey: "test_product", SignalIDs: []string{"watchdog/test"}}}
	row := evidence.Row{ID: "watchdog/test", Product: "test_product", Source: "sales", Kind: "metric", Metric: WatchdogMetric,
		Status: "observed", Value: "true", Ref: evidence.SourceRef{SourceID: "aifence-watchdog"}, Details: `{"origin":"imported_historical"}`}
	if id := realSignalID(draft, []evidence.Row{row}); id != row.ID {
		t.Fatalf("real alert not selected: %q", id)
	}

	for _, details := range []string{`{"origin":"synthetic_fixture"}`, `{"origin":"unknown"}`, `{"origin":"imported_historical","synthetic":true}`} {
		synthetic := row
		synthetic.Details = details
		if realSignalID(draft, []evidence.Row{synthetic}) != "" {
			t.Fatalf("ineligible provenance selected: %s", details)
		}
	}
	var reserved atomic.Bool
	for i := 0; i < 2; i++ {
		err := deliverSignalMail(ctx, func(context.Context) (bool, error) {
			return reserved.CompareAndSwap(false, true), nil
		}, func(context.Context) error {
			reserved.Store(false)
			return nil
		}, func(ctx context.Context) (bool, error) { return cfg.send(ctx, link, roots) })
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case msg := <-inbox:
		if msg.err != nil || !strings.Contains(msg.body, "To: qa@relay.test") || !strings.Contains(msg.body, link) {
			t.Fatalf("receiver did not get a usable message: %s, %v", msg.body, msg.err)
		}
	case <-ctx.Done():
		t.Fatal("no message received")
	}
	select {
	case msg := <-inbox:
		t.Fatalf("duplicate delivery: %v", msg)
	case <-time.After(50 * time.Millisecond):
	}
	u, err := url.Parse(link)
	if err != nil || u.Path != "/apps/product-manager/" || len(u.Query()) != 1 || u.Query().Get("id") != "watch/a+b" || u.Fragment != "" {
		t.Fatalf("bad card link: %s, %v", link, err)
	}
	t.Log("local TLS SMTP received one message for two delivery attempts")
}

func TestSignalMailReleasesOnlyBeforeData(t *testing.T) {
	for _, started := range []bool{false, true} {
		released := false
		failure := errors.New("SMTP reply lost")
		err := deliverSignalMail(t.Context(), func(context.Context) (bool, error) { return true, nil },
			func(context.Context) error { released = true; return nil },
			func(context.Context) (bool, error) { return started, failure })
		if !errors.Is(err, failure) || released == started {
			t.Fatalf("started = %t, released = %t, error = %v", started, released, err)
		}
	}
}

func smtpTestTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}},
		roots
}

func receiveSMTP(conn net.Conn, serverTLS *tls.Config) (string, error) {
	p := textproto.NewConn(conn)
	if err := p.PrintfLine("220 localhost test relay"); err != nil {
		return "", err
	}
	protected := false
	for {
		line, err := p.ReadLine()
		if err != nil {
			return "", err
		}
		switch command, _, _ := strings.Cut(line, " "); command {
		case "EHLO":
			if protected {
				err = p.PrintfLine("250 localhost")
			} else {
				err = p.PrintfLine("250-localhost\r\n250 STARTTLS")
			}
		case "STARTTLS":
			if err := p.PrintfLine("220 ready"); err != nil {
				return "", err
			}
			p = textproto.NewConn(tls.Server(conn, serverTLS))
			protected = true
		case "MAIL", "RCPT":
			if !protected {
				return "", errors.New("cleartext SMTP envelope")
			}
			err = p.PrintfLine("250 accepted")
		case "DATA":
			if err := p.PrintfLine("354 end with dot"); err != nil {
				return "", err
			}
			b, err := io.ReadAll(p.DotReader())
			if err != nil {
				return "", err
			}
			return string(b), p.PrintfLine("250 stored")
		default:
			return "", errors.New("unexpected SMTP command")
		}
		if err != nil {
			return "", err
		}
	}
}
