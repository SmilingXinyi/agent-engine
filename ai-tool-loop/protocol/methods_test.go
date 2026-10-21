package protocol

import (
	"encoding/json"
	"testing"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func TestModelCallCopiesSettingsAndReservesMetadata(t *testing.T) {
	temperature := 0.5
	maxTokens := 8
	message := aiprot.TextMessage(aiprot.RoleUser, "hi")
	message.ProviderOptions = map[string]any{"vendor_extension": map[string]any{"enabled": true}}
	request := Request{
		Model: "model", Messages: []aiprot.Message{message},
		Tools:           []Tool{{Definition: aiprot.ToolDefinition{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}},
		Temperature:     &temperature,
		MaxOutputTokens: &maxTokens,
		ToolChoice:      aiprot.ToolChoiceAuto,
		Reasoning:       &aiprot.ReasoningOptions{Effort: "medium"},
		OutputFormat:    &aiprot.OutputFormat{Type: aiprot.OutputJSON, Name: "answer", Schema: json.RawMessage(`{"type":"object"}`)},
		ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		Metadata:        map[string]string{"trace": "abc", MetadataRunID: "caller", aiprot.RequestIDMetadataKey: "caller-request", MetadataStep: "9"},
	}
	call := request.ModelCall(request.Messages, "run_1", "req_1", 2)
	call.ProviderOptions["thinking"].(map[string]any)["type"] = "changed"
	call.Messages[0].ProviderOptions["vendor_extension"].(map[string]any)["enabled"] = false
	call.OutputFormat.Schema[0] = 'x'
	call.Tools[0].InputSchema[0] = 'x'
	*call.Temperature = 1
	*call.MaxOutputTokens = 1
	call.Reasoning.Effort = "high"
	if request.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" || request.Messages[0].ProviderOptions["vendor_extension"].(map[string]any)["enabled"] != true || string(request.OutputFormat.Schema) != `{"type":"object"}` || string(request.Tools[0].Definition.InputSchema) != `{"type":"object"}` || *request.Temperature != 0.5 || *request.MaxOutputTokens != 8 || request.Reasoning.Effort != "medium" {
		t.Fatalf("request shared with model call: %#v", request)
	}
	if call.Model != "model" || call.ToolChoice != aiprot.ToolChoiceAuto || call.Metadata["trace"] != "abc" || call.Metadata[MetadataRunID] != "run_1" || call.Metadata[aiprot.RequestIDMetadataKey] != "req_1" || call.Metadata[MetadataStep] != "2" || len(call.Tools) != 1 {
		t.Fatalf("call=%#v", call)
	}
}
