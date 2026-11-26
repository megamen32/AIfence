package product

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk"
)

// PrivateReviewURL is deliberately independent of the user's stored records.
type PrivateReviewURL struct {
	URL string `json:"url"`
}

func PrivateReviewLink(ctx context.Context, _ evidence.Empty) (PrivateReviewURL, error) {
	base, err := agentsdk.AgentURLFromContext(ctx)
	if err != nil {
		return PrivateReviewURL{}, errors.New("адрес приложения Airlock временно недоступен")
	}
	return privateReviewURL(base)
}

func privateReviewURL(base string) (PrivateReviewURL, error) {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return PrivateReviewURL{}, errors.New("недопустимый адрес приложения Airlock")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return PrivateReviewURL{}, errors.New("недопустимый адрес приложения Airlock")
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	return PrivateReviewURL{URL: u.JoinPath("private-review").String()}, nil
}
