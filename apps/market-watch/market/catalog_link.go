package market

import (
	"context"
	"net/url"
	"strings"
)

// CatalogLink points to the caller-authenticated Baserow view selected for this deployment.
type CatalogLink struct {
	Location interface {
		Get(context.Context) (string, error)
	}
}

type CatalogDestination struct {
	URL        string `json:"url,omitempty"`
	Configured bool   `json:"configured"`
}

func (l CatalogLink) Open(ctx context.Context, _ struct{}) (CatalogDestination, error) {
	raw, err := l.Location.Get(ctx)
	if err != nil {
		return CatalogDestination{}, err
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" ||
		u.RawQuery != "" || !strings.HasPrefix(u.Path, "/database/") {
		return CatalogDestination{}, nil
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || strings.HasSuffix(host, ".localhost"))) {
		return CatalogDestination{}, nil
	}
	return CatalogDestination{URL: u.String(), Configured: true}, nil
}

func (l CatalogLink) URL(ctx context.Context) string {
	dest, _ := l.Open(ctx, struct{}{})
	return dest.URL
}
