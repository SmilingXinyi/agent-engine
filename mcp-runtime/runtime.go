// Package mcpruntime connects MCP servers and exposes their tools to ai-tool-loop.
package mcpruntime

import (
	"context"
	"errors"
	"fmt"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Runtime owns MCP connections. Close it after all executions finish.
type Runtime struct {
	Tools    []loopprot.Tool
	sessions []*sdk.ClientSession
}

// Start connects enabled servers in name order and discovers their enabled tools,
// including paginated lists. A failed startup closes every connection opened so far.
func Start(ctx context.Context, config protocol.Config) (*Runtime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	runtime := &Runtime{}
	client := sdk.NewClient(&sdk.Implementation{Name: "agent-engine", Version: "0.1.0"}, nil)
	seen := make(map[string]bool)
	for _, name := range config.ServerNames() {
		server := config.Servers[name]
		if server.Disabled {
			continue
		}
		session, err := client.Connect(ctx, buildTransport(server), nil)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("connect MCP server %q: %w", name, err), runtime.Close())
		}
		runtime.sessions = append(runtime.sessions, session)
		tools, err := sessionTools(ctx, name, session, server.DisabledTools)
		if err != nil {
			return nil, errors.Join(err, runtime.Close())
		}
		for _, tool := range tools {
			if seen[tool.Definition.Name] {
				return nil, errors.Join(fmt.Errorf("MCP tool name collision %q", tool.Definition.Name), runtime.Close())
			}
			seen[tool.Definition.Name] = true
		}
		runtime.Tools = append(runtime.Tools, tools...)
	}
	return runtime, nil
}

// Close releases sessions and their subprocesses. Repeated calls are harmless.
// It must not run concurrently with an agent execution or another Close.
func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	var err error
	for index := len(runtime.sessions) - 1; index >= 0; index-- {
		err = errors.Join(err, runtime.sessions[index].Close())
	}
	runtime.sessions = nil
	runtime.Tools = nil
	return err
}
