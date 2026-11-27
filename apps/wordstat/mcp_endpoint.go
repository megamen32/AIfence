package main

import (
	_ "embed"
	"strings"
)

// This public endpoint is a build-time declaration, independent of credentials.
// Change mcp-endpoint.txt and rebuild when deploying a different provider.
//
//go:embed mcp-endpoint.txt
var mcpEndpoint string

func configuredMCPEndpoint() string { return strings.TrimSpace(mcpEndpoint) }
