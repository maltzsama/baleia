package assistant

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolCaller is the assistant's only data path (#62): the in-process MCP
// client, so the policy gate and audit hooks apply unchanged.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (any, error)
}

// MCPToolCaller adapts an in-memory MCP client session.
type MCPToolCaller struct {
	session *mcpsdk.ClientSession
}

// NewMCPToolCaller wraps a session created by mcp.Server.InProcessClient.
func NewMCPToolCaller(session *mcpsdk.ClientSession) *MCPToolCaller {
	return &MCPToolCaller{session: session}
}

// CallTool runs one tool. A refusal is returned as structured content so the
// model can see it; a transport failure is an error.
func (c *MCPToolCaller) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	res, err := c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res.StructuredContent != nil {
		return res.StructuredContent, nil
	}
	return res.Content, nil
}
