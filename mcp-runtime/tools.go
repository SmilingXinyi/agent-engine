package mcpruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func sessionTools(ctx context.Context, serverName string, session *sdk.ClientSession, disabledTools []string) ([]loopprot.Tool, error) {
	var tools []loopprot.Tool
	disabled := make(map[string]bool, len(disabledTools))
	for _, name := range disabledTools {
		disabled[name] = true
	}
	seen := make(map[string]bool)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list MCP tools on %q: %w", serverName, err)
		}
		if tool == nil || tool.Name == "" {
			return nil, fmt.Errorf("MCP server %q returned an unnamed tool", serverName)
		}
		if disabled[tool.Name] {
			continue
		}
		definition, err := toolDefinition(serverName, tool)
		if err != nil {
			return nil, err
		}
		name := definition.Name
		if seen[name] {
			return nil, fmt.Errorf("MCP server %q returned duplicate tool %q", serverName, name)
		}
		seen[name] = true
		originalName := tool.Name
		tools = append(tools, loopprot.Tool{
			Definition: definition,
			Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
				// Keep JSON values intact, including integers beyond float64 precision.
				var object map[string]json.RawMessage
				if json.Unmarshal(arguments, &object) != nil || object == nil {
					return aiprot.ToolResult{}, fmt.Errorf("MCP tool %q arguments must be a JSON object", name)
				}
				response, err := session.CallTool(ctx, &sdk.CallToolParams{Name: originalName, Arguments: object})
				if err != nil {
					return aiprot.ToolResult{}, fmt.Errorf("call MCP tool %q: %w", name, err)
				}
				return toolResult(response)
			},
		})
	}
	return tools, nil
}

func toolDefinition(serverName string, tool *sdk.Tool) (aiprot.ToolDefinition, error) {
	name := serverName + "_" + tool.Name
	if len(name) > 64 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return aiprot.ToolDefinition{}, fmt.Errorf("MCP tool name %q must use 1-64 letters, digits, underscores or hyphens", name)
	}
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return aiprot.ToolDefinition{}, fmt.Errorf("encode MCP tool %q schema: %w", name, err)
	}
	var object map[string]any
	if json.Unmarshal(schema, &object) != nil || object == nil {
		return aiprot.ToolDefinition{}, fmt.Errorf("MCP tool %q input schema must be a JSON object", name)
	}
	if schemaType, exists := object["type"]; exists && schemaType != "object" {
		return aiprot.ToolDefinition{}, fmt.Errorf("MCP tool %q input schema type must be object", name)
	}
	return aiprot.ToolDefinition{Name: name, Description: tool.Description, InputSchema: schema}, nil
}

// toolResult retains text directly and serializes other MCP content as JSON
// text, so both SDK API formats can consume it without dropping content.
func toolResult(response *sdk.CallToolResult) (aiprot.ToolResult, error) {
	if response == nil {
		return aiprot.ToolResult{}, fmt.Errorf("MCP tool returned no result")
	}
	result := aiprot.ToolResult{IsError: response.IsError}
	for _, content := range response.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			result.Content = append(result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: text.Text})
			continue
		}
		data, err := json.Marshal(content)
		if err != nil {
			return aiprot.ToolResult{}, fmt.Errorf("encode MCP tool content: %w", err)
		}
		result.Content = append(result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: string(data)})
	}
	if response.StructuredContent != nil {
		data, err := json.Marshal(response.StructuredContent)
		if err != nil {
			return aiprot.ToolResult{}, fmt.Errorf("encode MCP structured content: %w", err)
		}
		result.Content = append(result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: string(data)})
	}
	return result, nil
}
