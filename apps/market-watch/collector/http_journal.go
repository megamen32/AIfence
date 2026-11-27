package catalog

import (
	"context"
	"time"
)

func NewArchivedHTTP(j *Journal) *HTTP {
	if j == nil {
		return NewHTTP()
	}
	return newHTTPWithArchive(func(_ context.Context, source, requested, actual string, status int, location string, at time.Time, body []byte) error {
		_, e := j.Archive(source, requested, actual, status, location, at, body)
		return e
	})
}
