package catalog

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func PublicStoreURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || u.Host != u.Hostname() {
		return fmt.Errorf("use an exact public HTTPS URL without credentials, port or fragment")
	}
	h := strings.ToLower(u.Hostname())
	if len(h) > 253 || !strings.Contains(h, ".") || !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`).MatchString(h) {
		return fmt.Errorf("public DNS hostname required")
	}
	if _, e := netip.ParseAddr(h); e == nil {
		return fmt.Errorf("IP literals not supported")
	}
	for _, s := range []string{".local", ".localhost", ".internal", ".home", ".lan", ".test", ".invalid", ".onion", ".arpa"} {
		if strings.HasSuffix(h, s) {
			return fmt.Errorf("non-public hostname")
		}
	}
	for k := range u.Query() {
		q := strings.ToLower(k)
		if strings.Contains(q, "token") || strings.Contains(q, "password") || strings.Contains(q, "auth") || strings.Contains(q, "session") || strings.Contains(q, "key") {
			return fmt.Errorf("credential-like query is not allowed")
		}
	}
	if len(raw) > 2048 {
		return fmt.Errorf("URL too long")
	}
	return nil
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}
func publicTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		if port != "443" {
			return nil, fmt.Errorf("only public HTTPS connections allowed")
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("empty DNS answer")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, fmt.Errorf("host resolves to non-public address")
			}
		}
		var last error
		for _, ip := range ips {
			d := net.Dialer{Timeout: 12 * time.Second}
			c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}
	return t
}
func NewPublicHTTP() *HTTP { return NewPublicArchivedHTTP(nil) }
func NewPublicArchivedHTTP(archive HTTPArchive) *HTTP {
	h := newHTTPWithArchive(archive)
	h.publicOnly = true
	h.transport = publicTransport()
	return h
}
