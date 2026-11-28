//go:build !airlock_private

package sourcewatch

import (
	"context"
	"errors"

	"github.com/airlockrun/agentsdk"
)

// HostSupportsAppRead reports whether this build can read a bound app tool
// from inside a job delivery. The published SDK v0.11.0 has no
// JobContext.ReadAppTool, so the public build answers with an honest refusal
// instead of pretending the source was read.
const HostSupportsAppRead = false

// jobReader returns the reader used for owner-scoped bound source reads.
func jobReader(job agentsdk.JobContext) Read {
	return func(ctx context.Context, binding string, input, output any) error {
		return errors.New("sourcewatch: cross-app source reads need the Airlock platform host; this build has no bound tool reader")
	}
}
