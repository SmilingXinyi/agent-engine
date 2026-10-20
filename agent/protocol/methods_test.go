package protocol_test

import (
	"reflect"
	"testing"

	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func callMessage(id, name string) aiprot.Message {
	return aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: id, Name: name}}}}
}

func resultMessage(id, name string) aiprot.Message {
	return aiprot.Message{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &aiprot.ToolResult{ToolCallID: id, ToolName: name}}}}
}

func TestHistoryRequiresCompleteMatchingToolBatches(t *testing.T) {
	call := callMessage("first", "lookup")
	result := resultMessage("first", "lookup")
	batch := callMessage("second", "other")
	batch.Parts = append(call.Parts[:1:1], batch.Parts...)
	valid := []aiprot.Message{aiprot.TextMessage(aiprot.RoleDeveloper, "rules"), aiprot.TextMessage(aiprot.RoleUser, "old"), batch, resultMessage("second", "other"), result, aiprot.TextMessage(aiprot.RoleAssistant, "done")}
	request := protocol.Request{Model: "local", History: valid, UserPrompt: "next"}
	before := request.LoopRequest().Messages
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, request.LoopRequest().Messages) {
		t.Fatal("validation modified history")
	}
	for _, history := range [][]aiprot.Message{
		{result}, {call}, {call, aiprot.TextMessage(aiprot.RoleUser, "next"), result},
		{call, resultMessage("unknown", "lookup")}, {call, resultMessage("first", "wrong")},
		{call, result, result}, {batch, result},
		{{Role: aiprot.RoleAssistant, Parts: append(call.Parts[:1:1], call.Parts...)}},
		{{Role: aiprot.RoleUser, Parts: call.Parts}}, {{Role: aiprot.RoleAssistant, Parts: result.Parts}},
		{{Role: aiprot.RoleTool}}, {aiprot.TextMessage(aiprot.RoleTool, "text")},
		{{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall}}}},
		{{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult}}}},
		{callMessage("", "lookup")}, {callMessage("first", "")}, {{Role: "unknown"}},
	} {
		if err := (protocol.Request{Model: "local", History: history, UserPrompt: "next"}).Validate(); err == nil {
			t.Fatalf("accepted history=%#v", history)
		}
	}
}
