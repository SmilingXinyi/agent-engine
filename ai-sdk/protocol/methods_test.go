package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestStructuredErrorPreservesClassificationAndCause(t *testing.T) {
	original := &Error{Code: ErrorTimeout, Provider: "custom", Message: "deadline", Cause: context.DeadlineExceeded, CallInfo: CallInfo{Attempts: 2}}
	wrapped := fmt.Errorf("call failed: %w", original)
	var failure *Error
	if !errors.As(wrapped, &failure) || failure.Code != ErrorTimeout || failure.CallInfo.Attempts != 2 || !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Fatalf("classification lost: %v", wrapped)
	}
}

func TestRequestCloneIsolatesNestedContentAndOptions(t *testing.T) {
	temperature := 0.5
	request := ModelRequest{
		Temperature:     &temperature,
		Messages:        []Message{{Role: RoleUser, Parts: []ContentPart{{Type: ContentProvider, Data: json.RawMessage(`{"type":"vendor"}`), Metadata: map[string]any{"nested": map[string]any{"value": "original"}}}}}},
		Tools:           []ToolDefinition{{Name: "tool", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ProviderOptions: map[string]any{"nested": []any{map[string]any{"value": "original"}}},
		OutputFormat:    &OutputFormat{Type: OutputJSONSchema, Name: "result", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	clone := request.Clone()
	clone.Messages[0].Parts[0].Data[0] = 'x'
	clone.Messages[0].Parts[0].Metadata["nested"].(map[string]any)["value"] = "changed"
	clone.ProviderOptions["nested"].([]any)[0].(map[string]any)["value"] = "changed"
	clone.Tools[0].InputSchema[0] = 'x'
	clone.OutputFormat.Schema[0] = 'x'
	*clone.Temperature = 1
	if !json.Valid(request.Messages[0].Parts[0].Data) || !json.Valid(request.Tools[0].InputSchema) || !json.Valid(request.OutputFormat.Schema) || *request.Temperature != 0.5 || request.ProviderOptions["nested"].([]any)[0].(map[string]any)["value"] != "original" || request.Messages[0].Parts[0].Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("clone mutated source: %#v", request)
	}
}

func TestResponseCloneIsolatesHeadersAndToolResults(t *testing.T) {
	result := TextToolResult("original")
	result.Metadata = map[string]any{"nested": map[string]any{"value": "original"}}
	response := ModelResponse{Message: Message{Parts: []ContentPart{{Type: ContentToolResult, ToolResult: &result}}}, CallInfo: CallInfo{Headers: map[string][]string{"X-Test": {"original"}}}, Raw: json.RawMessage(`{"original":true}`)}
	clone := response.Clone()
	clone.CallInfo.Headers["X-Test"][0] = "changed"
	clone.Message.Parts[0].ToolResult.Content[0].Text = "changed"
	clone.Message.Parts[0].ToolResult.Metadata["nested"].(map[string]any)["value"] = "changed"
	clone.Raw[0] = 'x'
	if response.CallInfo.Headers["X-Test"][0] != "original" || response.Message.Parts[0].ToolResult.Text() != "original" || result.Metadata["nested"].(map[string]any)["value"] != "original" || !json.Valid(response.Raw) {
		t.Fatalf("clone mutated source: %#v", response)
	}
}

func TestMessageOptionsAreIsolatedInAllSnapshots(t *testing.T) {
	original := TextMessage(RoleAssistant, "original")
	original.ProviderOptions = map[string]any{"nested": map[string]any{"values": []any{json.RawMessage(`{"original":true}`)}}}
	copies := []Message{
		original.Clone(),
		(ModelRequest{Messages: []Message{original}}).Clone().Messages[0],
		(ModelResponse{Message: original}).Clone().Message,
	}
	for _, copy := range copies {
		values := copy.ProviderOptions["nested"].(map[string]any)["values"].([]any)
		values[0].(json.RawMessage)[0] = 'x'
		values = append(values, "changed")
		copy.ProviderOptions["nested"].(map[string]any)["values"] = values
		if len(original.ProviderOptions["nested"].(map[string]any)["values"].([]any)) != 1 || !json.Valid(original.ProviderOptions["nested"].(map[string]any)["values"].([]any)[0].(json.RawMessage)) {
			t.Fatal("message options remained shared")
		}
	}
}
