// Package protocol defines MCP server connection configuration.
package protocol

import "net/http"

// Config describes the named servers whose tools are exposed to an agent.
// JSON uses mcpServers; ParseConfig also accepts the legacy servers key.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// ServerConfig selects Command for stdio or URL for Streamable HTTP / SSE.
// HTTPClient can supply caller-specific TLS and timeout policy.
type ServerConfig struct {
	// Type accepts stdio, http, or sse. Empty infers stdio from Command or
	// Streamable HTTP from URL, preserving configurations without a type.
	Type string `json:"type,omitempty"`
	// Disabled skips the connection and all tools, even when no transport is configured.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledTools matches original MCP tool names, before the server name prefix.
	DisabledTools []string          `json:"disabledTools,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Dir           string            `json:"dir,omitempty"`
	// MaxLineLength limits inbound stdio JSON-RPC frames in bytes. Zero uses
	// the SDK default (16 MiB). Negative values are rejected.
	MaxLineLength int               `json:"maxLineLength,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	HTTPClient    *http.Client      `json:"-"`
}
