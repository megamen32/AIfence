package catalog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func FailureType(err error, fallback string) (string, time.Time) {
	if err == nil {
		return fallback, time.Time{}
	}
	var he *HTTPStatus
	if errors.As(err, &he) {
		switch he.Code {
		case 403:
			return "forbidden", he.RetryAfter
		case 429:
			return "rate_limit", he.RetryAfter
		case 503:
			return "http_503", he.RetryAfter
		}
		if he.Code >= 500 {
			return "http_5xx", he.RetryAfter
		}
		return fmt.Sprintf("http_%d", he.Code), he.RetryAfter
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "robots.txt disallows"):
		return "robots_excluded", time.Time{}
	case strings.Contains(s, "minimum quantity"):
		return "parse_quantity_conflict", time.Time{}
	case strings.Contains(s, "context deadline"), strings.Contains(s, "timeout"), strings.Contains(s, "connection"), strings.Contains(s, "get \""):
		return "network", time.Time{}
	}
	return fallback, time.Time{}
}
