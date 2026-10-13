package mcpruntime

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

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const helperServerEnv = "AGENT_ENGINE_MCP_TEST_SERVER"

func TestMain(tests *testing.M) {
	if os.Getenv(helperServerEnv) == "1" {
		server := sdk.NewServer(&sdk.Implementation{Name: "stdio-test", Version: "1.0.0"}, nil)
		server.AddTool(&sdk.Tool{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			directory, err := os.Getwd()
			if err != nil {
				return nil, err
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: os.Getenv("MCP_TEST_VALUE") + "|" + directory}}}, nil
		})
		if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(tests.Run())
}

func TestDisabledServersAreNotConnectedOrStarted(t *testing.T) {
	var requests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer httpServer.Close()
	runtime, err := Start(testContext(t), protocol.Config{Servers: map[string]protocol.ServerConfig{
		"http":         {URL: httpServer.URL, Disabled: true},
		"stdio":        {Command: "/nonexistent/disabled-mcp-server", Disabled: true},
		"unconfigured": {Disabled: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if requests.Load() != 0 || len(runtime.sessions) != 0 || len(runtime.Tools) != 0 {
		t.Fatalf("requests=%d sessions=%d tools=%d", requests.Load(), len(runtime.sessions), len(runtime.Tools))
	}
}

type toolFilteringProvider struct {
	generate func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)
}

func (provider toolFilteringProvider) Name() string { return "filter-test" }

func (provider toolFilteringProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider.generate(ctx, request)
}

func TestDisabledToolsAreNotAdvertisedOrExecuted(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1.0.0"}, &sdk.ServerOptions{PageSize: 1})
	var reads, writes atomic.Int32
	for _, name := range []string{"read_file", "write_file"} {
		server.AddTool(&sdk.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			if name == "read_file" {
				reads.Add(1)
			} else {
				writes.Add(1)
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "done"}}}, nil
		})
	}
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	ctx := testContext(t)
	runtime, err := Start(ctx, protocol.Config{Servers: map[string]protocol.ServerConfig{
		"filesystem": {URL: httpServer.URL, DisabledTools: []string{"write_file", "not_present"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.Tools) != 1 || runtime.Tools[0].Definition.Name != "filesystem_read_file" {
		t.Fatalf("enabled tools = %#v", runtime.Tools)
	}
	modelCalls := 0
	provider := toolFilteringProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		modelCalls++
		if len(request.Tools) != 1 || request.Tools[0].Name != "filesystem_read_file" {
			t.Fatalf("model-visible tools = %#v", request.Tools)
		}
		if modelCalls == 1 {
			message := aiprot.Message{Role: aiprot.RoleAssistant}
			for _, name := range []string{"read_file", "write_file"} {
				call := aiprot.ToolCall{ID: name, Name: "filesystem_" + name, Arguments: json.RawMessage(`{}`)}
				message.Parts = append(message.Parts, aiprot.ContentPart{Type: aiprot.ContentToolCall, ToolCall: &call})
			}
			return aiprot.ModelResponse{Message: message, FinishReason: aiprot.FinishToolCalls}, nil
		}
		if modelCalls != 2 || len(request.Messages) != 4 {
			t.Fatalf("unexpected follow-up request = %#v", request)
		}
		readResult := request.Messages[2].ToolResults()[0]
		writeResult := request.Messages[3].ToolResults()[0]
		if readResult.IsError || readResult.Text() != "done" || !writeResult.IsError || !strings.Contains(writeResult.Text(), "unknown tool") {
			t.Fatalf("read=%#v write=%#v", readResult, writeResult)
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "finished")}, nil
	}}
	assistant := agent.New(ai.NewClient(provider, ai.Hooks{}))
	response, err := assistant.Execute(ctx, agentprot.Request{Model: "test", UserPrompt: "hello", Tools: runtime.Tools})
	if err != nil || response.LastResponse.Message.Text() != "finished" || reads.Load() != 1 || writes.Load() != 0 || modelCalls != 2 {
		t.Fatalf("response=%#v error=%v reads=%d writes=%d modelCalls=%d", response, err, reads.Load(), writes.Load(), modelCalls)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = Start(ctx, protocol.Config{Servers: map[string]protocol.ServerConfig{
		"filesystem": {URL: httpServer.URL, DisabledTools: []string{"read_file", "write_file"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.Tools) != 0 {
		t.Fatalf("all tools disabled, got %#v", runtime.Tools)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHTTPToolsCompleteAgentExecution(t *testing.T) {
	ctx := testContext(t)
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1.0.0"}, &sdk.ServerOptions{PageSize: 1})
	var toolCalls atomic.Int32
	server.AddTool(&sdk.Tool{Name: "greet", Description: "greet a user", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		toolCalls.Add(1)
		var arguments struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: "hello " + arguments.Name}},
			StructuredContent: map[string]any{"greeting": "hello " + arguments.Name},
		}, nil
	})
	server.AddTool(&sdk.Tool{Name: "fail", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		toolCalls.Add(1)
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "unavailable"}}}, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	mcpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("MCP authorization header was not forwarded")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	}))
	defer mcpServer.Close()
	runtime, err := Start(ctx, protocol.Config{Servers: map[string]protocol.ServerConfig{
		"demo": {URL: mcpServer.URL, Headers: map[string]string{"Authorization": "Bearer test-token"}, HTTPClient: mcpServer.Client()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.Tools) != 2 {
		t.Fatalf("paginated tools = %d, want 2", len(runtime.Tools))
	}
	var modelCalls atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Messages []struct {
				Role       string            `json:"role"`
				Content    string            `json:"content"`
				ToolCallID string            `json:"tool_call_id"`
				ToolCalls  []json.RawMessage `json:"tool_calls"`
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
		if len(payload.Tools) != 2 || payload.Tools[0].Function.Name != "demo_fail" || payload.Tools[1].Function.Name != "demo_greet" {
			t.Errorf("model tool definitions = %#v", payload.Tools)
		}
		writer.Header().Set("Content-Type", "application/json")
		if modelCalls.Add(1) == 1 {
			if len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[0].Content != "be helpful" {
				t.Errorf("initial messages = %#v", payload.Messages)
			}
			fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"greet-call","type":"function","function":{"name":"demo_greet","arguments":"{\"name\":\"Ada\"}"}},{"id":"fail-call","type":"function","function":{"name":"demo_fail","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
			return
		}
		if len(payload.Messages) != 5 {
			t.Errorf("follow-up messages = %#v", payload.Messages)
		} else {
			if payload.Messages[2].Role != "assistant" || len(payload.Messages[2].ToolCalls) != 2 {
				t.Error("assistant tool calls were not retained")
			}
			greet, failure := payload.Messages[3], payload.Messages[4]
			if greet.Role != "tool" || greet.ToolCallID != "greet-call" || !strings.Contains(greet.Content, "hello Ada") || !strings.Contains(greet.Content, `"greeting"`) {
				t.Errorf("greet result = %#v", greet)
			}
			if failure.Role != "tool" || failure.ToolCallID != "fail-call" || !strings.Contains(failure.Content, "unavailable") {
				t.Errorf("failure result = %#v", failure)
			}
		}
		fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"hello Ada"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`)
	}))
	defer modelServer.Close()
	provider, err := openai.New(openai.Config{BaseURL: modelServer.URL, HTTPClient: modelServer.Client(), MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	assistant := agent.New(ai.NewClient(provider, ai.Hooks{}))
	response, err := assistant.Execute(ctx, agentprot.Request{
		Model: "test-model", SystemPrompt: "be helpful", UserPrompt: "greet Ada", Tools: runtime.Tools,
	})
	if err != nil || response.LastResponse.Message.Text() != "hello Ada" || response.Usage.TotalTokens != 17 || modelCalls.Load() != 2 || toolCalls.Load() != 2 {
		t.Fatalf("response=%#v err=%v modelCalls=%d toolCalls=%d", response, err, modelCalls.Load(), toolCalls.Load())
	}
}

func TestStdioToolUsesEnvironmentAndClosesProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_TEST_VALUE", "inherited")
	runtime, err := Start(testContext(t), protocol.Config{Servers: map[string]protocol.ServerConfig{
		"local": {Command: executable, Args: []string{"-test.run=^$"}, Dir: directory, Env: map[string]string{helperServerEnv: "1", "MCP_TEST_VALUE": "override"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.Tools) != 1 || runtime.Tools[0].Definition.Name != "local_echo" {
		t.Fatalf("stdio tools = %#v", runtime.Tools)
	}
	execute := runtime.Tools[0].Execute
	result, err := execute(testContext(t), json.RawMessage(`{}`))
	if err != nil || result.Text() != "override|"+resolvedDirectory {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(testContext(t), json.RawMessage(`{}`)); err == nil {
		t.Fatal("tool should fail after its session is closed")
	}
}

func TestFailedStartupClosesPreviouslyConnectedServer(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	var deletes atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes.Add(1)
		}
		handler.ServeHTTP(writer, request)
	}))
	defer httpServer.Close()
	runtime, err := Start(testContext(t), protocol.Config{Servers: map[string]protocol.ServerConfig{
		"a": {URL: httpServer.URL},
		"b": {Command: "/nonexistent/agent-engine-mcp-test-server"},
	}})
	if err == nil || runtime != nil || deletes.Load() != 1 {
		t.Fatalf("runtime=%#v error=%v session deletes=%d", runtime, err, deletes.Load())
	}
}

func TestMultipleServersQualifyNamesAndRejectCollisions(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(fmt.Sprintf("collision=%v", collision), func(t *testing.T) {
			config := protocol.Config{Servers: make(map[string]protocol.ServerConfig)}
			for _, name := range []string{"a", "a_b"} {
				toolName := "echo"
				if collision {
					toolName = "c"
					if name == "a" {
						toolName = "b_c"
					}
				}
				server := sdk.NewServer(&sdk.Implementation{Name: name, Version: "1.0.0"}, nil)
				server.AddTool(&sdk.Tool{Name: toolName, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: toolName}}}, nil
				})
				httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
				defer httpServer.Close()
				config.Servers[name] = protocol.ServerConfig{URL: httpServer.URL}
			}
			runtime, err := Start(testContext(t), config)
			if collision {
				if err == nil || !strings.Contains(err.Error(), "collision") || runtime != nil {
					t.Fatalf("runtime=%#v error=%v", runtime, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			if len(runtime.Tools) != 2 || runtime.Tools[0].Definition.Name != "a_echo" || runtime.Tools[1].Definition.Name != "a_b_echo" {
				t.Fatalf("qualified tools = %#v", runtime.Tools)
			}
			for _, tool := range runtime.Tools {
				result, err := tool.Execute(testContext(t), json.RawMessage(`{}`))
				if err != nil || result.Text() != "echo" {
					t.Fatalf("original MCP tool name was not retained: result=%#v error=%v", result, err)
				}
			}
		})
	}
}
