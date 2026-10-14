package mcpruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolDefinitionRejectsNonObjectSchemas(t *testing.T) {
	for _, schema := range []string{`null`, `[]`, `"string"`, `{"type":"array"}`, `{"type":null}`, `{"type":["object"]}`, `{`} {
		if _, err := toolDefinition("demo", &sdk.Tool{Name: "echo", InputSchema: json.RawMessage(schema)}); err == nil {
			t.Fatalf("accepted schema %s", schema)
		}
	}
	for _, schema := range []string{`{}`, `{"type":"object"}`} {
		definition, err := toolDefinition("demo", &sdk.Tool{Name: "echo", InputSchema: json.RawMessage(schema)})
		if err != nil || definition.Name != "demo_echo" {
			t.Fatalf("definition=%#v error=%v", definition, err)
		}
	}
}

func TestMCPArgumentsRetainLargeIntegers(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "precision-test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(request.Params.Arguments)}}}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	runtime, err := Start(testContext(t), protocol.Config{Servers: map[string]protocol.ServerConfig{"demo": {URL: httpServer.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	result, err := runtime.Tools[0].Execute(testContext(t), json.RawMessage(`{"integer":9007199254740993,"nested":{"value":18446744073709551615}}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Text()), &output); err != nil {
		t.Fatal(err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(output["nested"], &nested); err != nil {
		t.Fatal(err)
	}
	if string(output["integer"]) != "9007199254740993" || string(nested["value"]) != "18446744073709551615" {
		t.Fatal(result.Text())
	}
}
