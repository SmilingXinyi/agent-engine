package protocol

import (
	"encoding/json"
	"reflect"
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

func TestModelCallKeepsRecentCompleteRoundsAndAllSystemMessages(t *testing.T) {
	toolBatch := func(names ...string) []aiprot.Message {
		assistant := aiprot.Message{Role: aiprot.RoleAssistant}
		var results []aiprot.Message
		for _, name := range names {
			call := aiprot.ToolCall{ID: name, Name: "lookup", Arguments: json.RawMessage(`{}`)}
			assistant.Parts = append(assistant.Parts, aiprot.ContentPart{Type: aiprot.ContentToolCall, ToolCall: &call})
			result := aiprot.TextToolResult("result " + name)
			result.ToolCallID, result.ToolName = name, "lookup"
			results = append(results, aiprot.Message{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}})
		}
		return append([]aiprot.Message{assistant}, results...)
	}
	messages := []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleSystem, "system one"),
		aiprot.TextMessage(aiprot.RoleDeveloper, "initial instructions"),
		aiprot.TextMessage(aiprot.RoleUser, "first input"),
	}
	messages = append(messages, toolBatch("first-a", "first-b")...)
	messages = append(messages,
		aiprot.TextMessage(aiprot.RoleSystem, "system two"),
		aiprot.TextMessage(aiprot.RoleAssistant, "second round"),
		aiprot.TextMessage(aiprot.RoleUser, "third input"),
	)
	messages = append(messages, toolBatch("third-a", "third-b")...)
	messages = append(messages, aiprot.TextMessage(aiprot.RoleUser, "current input"))
	for _, scenario := range []struct {
		name    string
		limit   int
		indices []int
	}{
		{name: "current-input-only", limit: 1, indices: []int{0, 6, 12}},
		{name: "two-rounds-with-whole-tool-batch", limit: 2, indices: []int{0, 6, 8, 9, 10, 11, 12}},
		{name: "consecutive-assistant-is-another-round", limit: 3, indices: []int{0, 6, 7, 8, 9, 10, 11, 12}},
		{name: "exact-boundary", limit: 4},
		{name: "default", limit: 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := Request{Model: "test", Messages: messages, Options: RunOptions{MaxHistoryRounds: scenario.limit}}
			original := request.Clone()
			modelCall := request.ModelCall(messages, "run", "request", 1)
			want := messages
			if scenario.indices != nil {
				want = nil
				for _, index := range scenario.indices {
					want = append(want, messages[index])
				}
			}
			if !reflect.DeepEqual(modelCall.Messages, want) {
				t.Fatalf("retained messages=%+v, want %+v", modelCall.Messages, want)
			}
			if !reflect.DeepEqual(request, original) {
				t.Fatal("history window changed the original request")
			}
		})
	}
	// Each complete two-tool batch still counts as just one round.
	request := Request{Options: RunOptions{MaxHistoryRounds: 1}}
	if got := request.ModelCall(messages[:6], "run", "request", 1).Messages; !reflect.DeepEqual(got, messages[:6]) {
		t.Fatal("a single round was split by the number of tool results")
	}
}

func TestModelCallDefaultHistoryWindow(t *testing.T) {
	messages := []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, "rules")}
	for index := 0; index < DefaultMaxHistoryRounds+2; index++ {
		messages = append(messages, aiprot.TextMessage(aiprot.RoleAssistant, "round"))
	}
	got := (Request{}).ModelCall(messages, "run", "request", 1).Messages
	if len(got) != DefaultMaxHistoryRounds+1 || got[0].Role != aiprot.RoleSystem {
		t.Fatalf("default window retained %d messages", len(got))
	}
}
