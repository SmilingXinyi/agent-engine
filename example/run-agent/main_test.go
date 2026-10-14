package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the real example entrypoint against local model/MCP servers. No
// external API credentials, npx subprocess or connected mobile device is used.
func TestRunWithLocalModelAndMCP(t *testing.T) {
	for _, mode := range []string{"success", "skills", "permission failure", "max steps"} {
		t.Run(mode, func(t *testing.T) {
			for _, name := range []string{"AGENT_MODEL", "AGENT_LLM_MODEL", "AGENT_LLM_TOKEN", "AGENT_LLM_API_KEY", "AGENT_LLM_BASE_URL"} {
				t.Setenv(name, "")
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			var toolCalls, modelCalls atomic.Int32
			server := sdk.NewServer(&sdk.Implementation{Name: "local-test", Version: "1.0.0"}, nil)
			server.AddTool(&sdk.Tool{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				toolCalls.Add(1)
				if mode == "permission failure" {
					return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "INJECT_EVENTS permission denied"}}}, nil
				}
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "hello"}}}, nil
			})
			mcpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
			defer mcpServer.Close()
			modelServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("example did not load test credentials")
				}
				var payload struct {
					Model    string `json:"model"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					Tools []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload.Model != "local-model" {
					t.Errorf("model=%q", payload.Model)
				}
				writer.Header().Set("Content-Type", "application/json")
				callNumber := modelCalls.Add(1)
				if mode == "skills" && callNumber == 1 {
					if len(payload.Tools) != 2 || payload.Tools[1].Function.Name != "get_skill" || !strings.Contains(payload.Messages[0].Content, "demo description") || strings.Contains(payload.Messages[0].Content, "Inspect before clicking") {
						t.Errorf("skills were not progressively exposed: %#v", payload)
					}
					fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"skill-call","type":"function","function":{"name":"get_skill","arguments":"{\"name\":\"demo\"}"}}]},"finish_reason":"tool_calls"}]}`)
					return
				}
				if callNumber == 1 || (mode == "skills" && callNumber == 2) {
					if mode == "skills" {
						if len(payload.Messages) != 4 || !strings.Contains(payload.Messages[3].Content, "Inspect before clicking") {
							t.Errorf("skill result=%#v", payload.Messages)
						}
					} else {
						if len(payload.Messages) != 2 || payload.Messages[1].Content != "test prompt" {
							t.Errorf("messages=%#v", payload.Messages)
						}
					}
					fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"echo-call","type":"function","function":{"name":"mobile-mcp_echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
					return
				}
				messageCount := 4
				if mode == "skills" {
					messageCount = 6
				}
				if len(payload.Messages) != messageCount || payload.Messages[len(payload.Messages)-1].Role != "tool" || payload.Messages[len(payload.Messages)-1].Content != "hello" {
					t.Errorf("follow-up=%#v", payload.Messages)
				}
				fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
			}))
			defer modelServer.Close()
			directory := t.TempDir()
			config, err := json.Marshal(mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{"mobile-mcp": {URL: mcpServer.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{
				"mcp.json":  config,
				"prompt.md": []byte("test prompt"),
				".env":      []byte("AGENT_LLM_MODEL=\"local-model\"\nAGENT_LLM_API_KEY='test-key'\nAGENT_LLM_BASE_URL=" + modelServer.URL + "\n"),
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(directory, name), content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			maxSteps := 4
			skillsPath := ""
			if mode == "skills" {
				skillsPath = filepath.Join(directory, "skills")
				if err := os.MkdirAll(filepath.Join(skillsPath, "demo"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillsPath, "demo", "SKILL.md"), []byte("---\nname: demo\ndescription: demo description\n---\nInspect before clicking"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "max steps" {
				maxSteps = 1
			}
			err = run(filepath.Join(directory, "prompt.md"), filepath.Join(directory, "mcp.json"), filepath.Join(directory, ".env"), skillsPath, maxSteps, 10*time.Second)
			switch mode {
			case "success":
				if err != nil || modelCalls.Load() != 2 || toolCalls.Load() != 1 {
					t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
				}
			case "skills":
				if err != nil || modelCalls.Load() != 3 || toolCalls.Load() != 1 {
					t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
				}
			case "permission failure":
				if err == nil || !strings.Contains(err.Error(), "mobile input permission") || modelCalls.Load() != 1 || toolCalls.Load() != 1 {
					t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
				}
			case "max steps":
				if err == nil || !strings.Contains(err.Error(), "limit reached") || modelCalls.Load() != 1 || toolCalls.Load() != 0 {
					t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
				}
			}
		})
	}
}
