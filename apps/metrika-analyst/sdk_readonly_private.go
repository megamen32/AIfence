//go:build airlock_private

package main

import "github.com/airlockrun/agentsdk"

// readOnlyTool publishes the manifest read-only bit that the private Airlock
// platform SDK exposes. The build tag is set by scripts/personal-build-apps.py
// together with the pinned private SDK replacement.
func readOnlyTool() agentsdk.RegisterOption {
	return agentsdk.WithReadOnly()
}
