package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai/mcp"
)

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// MCPServerConfig describes how to connect to one MCP server.
type MCPServerConfig struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	URL       string            `json:"url,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type MCPRuntime struct {
	Tools   []agent.Tool
	Clients []*mcp.Client
}

func LoadMCPConfigFile(path string) (MCPConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MCPConfig{Servers: map[string]MCPServerConfig{}}, nil
		}
		return MCPConfig{}, fmt.Errorf("read mcp config %s: %w", path, err)
	}

	var config MCPConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return MCPConfig{}, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	if config.Servers == nil {
		config.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range config.Servers {
		if err := validateMCPServerConfig(name, server); err != nil {
			return MCPConfig{}, err
		}
	}
	return config, nil
}

func StartMCPRuntime(ctx context.Context, config MCPConfig) (*MCPRuntime, error) {
	runtime := &MCPRuntime{}
	if len(config.Servers) == 0 {
		return runtime, nil
	}

	serverNames := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, name := range serverNames {
		server := config.Servers[name]
		client, tools, err := startMCPServer(ctx, name, server)
		if err != nil {
			return nil, errors.Join(err, wrapMCPCloseError("partially started runtime", runtime.Close()))
		}
		runtime.Clients = append(runtime.Clients, client)
		runtime.Tools = append(runtime.Tools, tools...)
	}
	return runtime, nil
}

func (runtime *MCPRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	var closeErr error
	for _, client := range runtime.Clients {
		if err := client.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func validateMCPServerConfig(name string, server MCPServerConfig) error {
	hasCommand := strings.TrimSpace(server.Command) != ""
	hasURL := strings.TrimSpace(server.URL) != ""
	transport := strings.TrimSpace(server.Transport)
	if hasCommand == hasURL {
		return fmt.Errorf("mcp server %q must configure exactly one of command or url", name)
	}
	if hasCommand && transport != "" {
		return fmt.Errorf("mcp server %q transport is only valid with url", name)
	}
	if hasURL {
		parsedURL, err := url.Parse(server.URL)
		if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
			return fmt.Errorf("mcp server %q url is invalid", name)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("mcp server %q url must use http or https", name)
		}
		if transport != "" && transport != "http" && transport != "sse" {
			return fmt.Errorf("mcp server %q transport must be http or sse", name)
		}
		for headerName := range server.Headers {
			if !isValidHTTPHeaderName(headerName) {
				return fmt.Errorf("mcp server %q header %q is invalid", name, headerName)
			}
		}
	}
	return nil
}

func isValidHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char > unicode.MaxASCII || !(unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("!#$%&'*+-./=?^_`{|}~", char)) {
			return false
		}
	}
	return true
}

func buildMCPTransport(server MCPServerConfig) mcp.Transport {
	if server.URL != "" {
		if strings.TrimSpace(server.Transport) == "http" {
			return mcp.NewHTTPTransport(server.URL, mcp.WithHTTPHeaders(server.Headers))
		}
		return NewRelativeSSETransport(server.URL, server.Headers)
	}

	options := make([]mcp.StdioOption, 0, 2)
	if len(server.Env) > 0 {
		options = append(options, mcp.WithStdioEnv(server.Env))
	}
	if server.Dir != "" {
		options = append(options, mcp.WithStdioDir(server.Dir))
	}
	return mcp.NewStdioTransport(server.Command, server.Args, options...)
}

func startMCPServer(ctx context.Context, name string, server MCPServerConfig) (*mcp.Client, []agent.Tool, error) {
	transport := buildMCPTransport(server)
	client := mcp.NewClient("agent-engine-"+name, "1.0.0", mcp.WithTransport(transport), mcp.WithRequestTimeout(60*time.Second))
	if err := client.Connect(ctx); err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("connect mcp server %q: %w", name, err),
			wrapMCPCloseError(name, client.Close()),
		)
	}

	mcpTools, err := listAllMCPTools(ctx, client)
	if err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("list mcp tools %q: %w", name, err),
			wrapMCPCloseError(name, client.Close()),
		)
	}
	validatedTools, err := validateMCPTools(name, mcpTools)
	if err != nil {
		return nil, nil, errors.Join(err, wrapMCPCloseError(name, client.Close()))
	}
	return client, convertMCPTools(client, validatedTools), nil
}

func convertMCPTools(client *mcp.Client, mcpTools []mcp.Tool) []agent.Tool {
	tools := make([]agent.Tool, 0, len(mcpTools))
	for _, mcpTool := range mcpTools {
		tool := mcpTool
		tools = append(tools, agent.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
			Handler: agent.ToolHandlerFunc(func(ctx context.Context, input json.RawMessage) (string, error) {
				args := make(map[string]any)
				if len(input) > 0 {
					if err := json.Unmarshal(input, &args); err != nil {
						return "", err
					}
				}
				result, err := client.CallTool(ctx, tool.Name, args)
				if err != nil {
					return "", err
				}
				return formatMCPToolResult(tool.Name, result)
			}),
		})
	}
	return tools
}

func formatMCPToolResult(toolName string, result *mcp.CallToolResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("mcp tool %q returned no result", toolName)
	}
	output := mcp.FormatContent(result.Content, result.IsError)
	if result.IsError {
		return output, fmt.Errorf("mcp tool %q failed: %s", toolName, output)
	}
	return output, nil
}

func wrapMCPCloseError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close mcp %s: %w", name, err)
}

func validateMCPTools(serverName string, tools []mcp.Tool) ([]mcp.Tool, error) {
	seenNames := make(map[string]struct{}, len(tools))
	validatedTools := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if !isValidMCPToolName(tool.Name) {
			return nil, fmt.Errorf("mcp server %q returned invalid tool name %q", serverName, tool.Name)
		}
		if _, ok := seenNames[tool.Name]; ok {
			return nil, fmt.Errorf("mcp server %q returned duplicate tool name %q", serverName, tool.Name)
		}
		seenNames[tool.Name] = struct{}{}
		if len(tool.InputSchema) == 0 {
			tool.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		if !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("mcp server %q tool %q input schema is invalid json", serverName, tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf("mcp server %q tool %q input schema must be a json object", serverName, tool.Name)
		}
		if schema == nil {
			return nil, fmt.Errorf("mcp server %q tool %q input schema must be a json object", serverName, tool.Name)
		}
		if schemaType, ok := schema["type"]; ok && schemaType != "object" {
			return nil, fmt.Errorf("mcp server %q tool %q input schema type must be object", serverName, tool.Name)
		}
		validatedTools = append(validatedTools, tool)
	}
	return validatedTools, nil
}

func isValidMCPToolName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if char > unicode.MaxASCII || !(unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '-' || char == '.') {
			return false
		}
	}
	return true
}

func listAllMCPTools(ctx context.Context, client *mcp.Client) ([]mcp.Tool, error) {
	result, err := client.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	tools := append([]mcp.Tool{}, result.Tools...)
	for result.NextCursor != "" {
		result, err = client.ListTools(ctx, &mcp.ListParams{Cursor: result.NextCursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
	}
	return tools, nil
}
