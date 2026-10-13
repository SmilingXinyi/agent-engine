// Package protocol defines MCP server connection configuration.
package protocol

import "net/http"

// Config describes the named servers whose tools are exposed to an agent.
// JSON uses mcpServers; ParseConfig also accepts the legacy servers key.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// ServerConfig selects exactly one transport: Command for stdio or URL for
// Streamable HTTP. HTTPClient can supply caller-specific TLS and timeout policy.
type ServerConfig struct {
	// Disabled skips the connection and all tools, even when no transport is configured.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledTools matches original MCP tool names, before the server name prefix.
	DisabledTools []string          `json:"disabledTools,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Dir           string            `json:"dir,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	HTTPClient    *http.Client      `json:"-"`
}
