//go:build airlock_private

package sourcewatch

import (
	"context"

	"github.com/airlockrun/agentsdk"
)

// HostSupportsAppRead reports whether this build can read a bound app tool
// from inside a job delivery. The private platform SDK exposes
// JobContext.ReadAppTool, which resolves the owner and checks the live grant
// for every delivery.
const HostSupportsAppRead = true

// jobReader returns the reader used for owner-scoped bound source reads.
// ReadAppTool requires the active leased job attempt and the owner's exact
// binding; no alternate credential or manual read path is used.
func jobReader(job agentsdk.JobContext) Read {
	return func(ctx context.Context, binding string, input, output any) error {
		return job.ReadAppTool(ctx, binding, input, output)
	}
}
