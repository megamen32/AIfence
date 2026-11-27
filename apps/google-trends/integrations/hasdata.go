package integrations

import (
	"agent/trends"
	"context"
	"encoding/json"
	"errors"
	"github.com/airlockrun/agentsdk"
)

type HasData struct{ mcp *agentsdk.MCPHandle }

func NewHasData(mcp *agentsdk.MCPHandle) *HasData { return &HasData{mcp: mcp} }
func (h *HasData) Call(ctx context.Context, args map[string]any) ([]byte, error) {
	response, err := h.mcp.CallTool(ctx, trends.UpstreamTool, args)
	if err != nil {
		if _, required := agentsdk.IsAuthRequired(err); required {
			return nil, trends.ErrCredentialsRequired
		}
		var httpErr *agentsdk.ConnectionHTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.StatusCode {
			case 401:
				return nil, trends.ErrCredentialsRequired
			case 402:
				return nil, trends.ErrQuotaExhausted
			case 403:
				return nil, trends.ErrAccessDenied
			case 429:
				return nil, trends.ErrRateLimited
			}
		}
		return nil, errors.New("MCP transport failed")
	}
	return json.Marshal(response)
}
