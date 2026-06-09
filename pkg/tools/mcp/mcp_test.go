package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zendev-sh/goai/mcp"
)

func TestLoadMCPConfigFileMissingFileReturnsEmptyConfig(t *testing.T) {
	config, err := LoadMCPConfigFile(filepath.Join(t.TempDir(), "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Servers) != 0 {
		t.Fatalf("expected no servers, got %#v", config.Servers)
	}
}

func TestLoadMCPConfigFileParsesServers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	writeTestFile(t, path, `{
		"servers": {
			"filesystem": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/workspace"],
				"env": {"FOO": "bar"},
				"dir": "/tmp"
			}
		}
	}`)

	config, err := LoadMCPConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["filesystem"]
	if server.Command != "npx" {
		t.Fatalf("unexpected command: %q", server.Command)
	}
	if got := strings.Join(server.Args, ","); got != "-y,@modelcontextprotocol/server-filesystem,/tmp/workspace" {
		t.Fatalf("unexpected args: %q", got)
	}
	if server.Env["FOO"] != "bar" {
		t.Fatalf("unexpected env: %#v", server.Env)
	}
	if server.Dir != "/tmp" {
		t.Fatalf("unexpected dir: %q", server.Dir)
	}
}

func TestLoadMCPConfigFileRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{`)

	_, err := LoadMCPConfigFile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse mcp config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMCPConfigFileParsesHTTPServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{
		"servers": {
			"remote": {
				"url": "https://mcp.example.com/mcp",
				"headers": {"Authorization": "Bearer token"}
			}
		}
	}`)

	config, err := LoadMCPConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["remote"]
	if server.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("unexpected url: %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer token" {
		t.Fatalf("unexpected headers: %#v", server.Headers)
	}
}

func TestLoadMCPConfigFileParsesSSEServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{
		"servers": {
			"remote": {
				"url": "https://mcp.example.com/mcp",
				"transport": "sse"
			}
		}
	}`)

	config, err := LoadMCPConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["remote"]
	if server.Transport != "sse" {
		t.Fatalf("unexpected transport: %q", server.Transport)
	}
}

func TestLoadMCPConfigFileRejectsMissingCommandAndURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{"servers":{"filesystem":{"args":["-y"]}}}`)

	_, err := LoadMCPConfigFile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "filesystem") {
		t.Fatalf("expected server name in error, got %v", err)
	}
}

func TestLoadMCPConfigFileRejectsCommandAndURLTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{"servers":{"mixed":{"command":"npx","url":"https://mcp.example.com/mcp"}}}`)

	_, err := LoadMCPConfigFile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMCPConfigFileRejectsInvalidHTTPURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{"servers":{"remote":{"url":"ftp://mcp.example.com/mcp"}}}`)

	_, err := LoadMCPConfigFile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMCPTools(t *testing.T) {
	validTools, err := validateMCPTools("remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(validTools) != 0 {
		t.Fatalf("expected no tools, got %#v", validTools)
	}
}

func TestValidateMCPToolsRejectsInvalidName(t *testing.T) {
	_, err := validateMCPTools("remote", []mcp.Tool{{Name: "bad name"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid tool name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMCPToolsRejectsDuplicateName(t *testing.T) {
	_, err := validateMCPTools("remote", []mcp.Tool{{Name: "read_file"}, {Name: "read_file"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMCPToolsRejectsInvalidSchema(t *testing.T) {
	_, err := validateMCPTools("remote", []mcp.Tool{{Name: "read_file", InputSchema: []byte(`{"type":"array"}`)}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "type must be object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMCPToolsDefaultsEmptySchema(t *testing.T) {
	validTools, err := validateMCPTools("remote", []mcp.Tool{{Name: "read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(validTools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("unexpected default schema: %s", validTools[0].InputSchema)
	}
}

func TestStartMCPRuntimeIntegration(t *testing.T) {
	if os.Getenv("AGENT_MCP_INTEGRATION_TEST") != "1" {
		t.Skip("set AGENT_MCP_INTEGRATION_TEST=1 to run MCP integration test")
	}

	root := t.TempDir()
	config := MCPConfig{Servers: map[string]MCPServerConfig{
		"filesystem": {Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", root}},
	}}

	runtime, err := StartMCPRuntime(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.Tools) == 0 {
		t.Fatal("expected MCP tools")
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
