//go:build !airlock_private

package main

import "github.com/airlockrun/agentsdk"

// readOnlyTool declares a source-reading tool.
//
// The published SDK v0.11.0 cannot publish the manifest read-only bit: it has
// no agentsdk.WithReadOnly option and no read-only field in the wire tool
// definition. The contract is therefore carried by the tool description and by
// a model hint on hosts that use the public SDK. The private platform build
// compiles sdk_readonly_private.go and keeps the manifest bit, so a source
// binding stays marked read-only exactly as before.
func readOnlyTool() agentsdk.RegisterOption {
	return agentsdk.WithLLMHint("Read-only source operation: this tool only reads evidence and never changes state.")
}
