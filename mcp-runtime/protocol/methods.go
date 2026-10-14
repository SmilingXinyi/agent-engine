package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
)

// LoadConfig reads and validates an MCP JSON file without starting any servers.
func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read MCP config %q: %w", path, err)
	}
	config, err := ParseConfig(string(content))
	if err != nil {
		return Config{}, fmt.Errorf("load MCP config %q: %w", path, err)
	}
	return config, nil
}

// ParseConfig validates a JSON document with a mcpServers object. The legacy
// servers key is accepted, but the two keys must not be supplied together.
func ParseConfig(content string) (Config, error) {
	var document struct {
		MCPServers json.RawMessage `json:"mcpServers"`
		Servers    json.RawMessage `json:"servers"`
	}
	if err := decodeConfigJSON(content, &document); err != nil {
		return Config{}, fmt.Errorf("parse MCP config: %w", err)
	}
	if len(document.MCPServers) > 0 && len(document.Servers) > 0 {
		return Config{}, fmt.Errorf("MCP config must not contain both mcpServers and servers")
	}
	servers := document.MCPServers
	if len(servers) == 0 {
		servers = document.Servers
	}
	var config Config
	if len(servers) == 0 {
		return Config{}, fmt.Errorf("MCP config requires a mcpServers object")
	}
	if err := decodeConfigJSON(string(servers), &config.Servers); err != nil {
		return Config{}, fmt.Errorf("parse MCP servers: %w", err)
	}
	if config.Servers == nil {
		return Config{}, fmt.Errorf("MCP config mcpServers must be an object")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func decodeConfigJSON(content string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON document")
	}
	return nil
}

// ServerNames returns server names in connection order.
func (config Config) ServerNames() []string {
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate checks all servers before any connection or subprocess is started.
func (config Config) Validate() error {
	for _, name := range config.ServerNames() {
		if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
			return fmt.Errorf("invalid MCP server name %q", name)
		}
		server := config.Servers[name]
		for _, toolName := range server.DisabledTools {
			if strings.TrimSpace(toolName) == "" || strings.TrimSpace(toolName) != toolName {
				return fmt.Errorf("MCP server %q disabled tool names must not be empty or have surrounding whitespace", name)
			}
		}
		if server.Disabled {
			continue
		}
		hasCommand := strings.TrimSpace(server.Command) != ""
		hasURL := strings.TrimSpace(server.URL) != ""
		if hasCommand == hasURL {
			return fmt.Errorf("MCP server %q must configure exactly one of command or url", name)
		}
		if hasURL {
			parsed, err := url.Parse(server.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fmt.Errorf("MCP server %q requires an HTTP or HTTPS URL", name)
			}
			for header, value := range server.Headers {
				if header == "" || strings.Trim(header, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&'*+-.^_`|~") != "" {
					return fmt.Errorf("MCP server %q has an invalid HTTP header name %q", name, header)
				}
				for _, character := range value {
					if (character < ' ' && character != '\t') || character == '\x7f' {
						return fmt.Errorf("MCP server %q header %q contains an invalid control character", name, header)
					}
				}
			}
		}
	}
	return nil
}
