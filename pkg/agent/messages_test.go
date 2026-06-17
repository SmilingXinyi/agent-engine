package agent

import (
	"strings"
	"testing"
)

func TestNormalizeHistoryMessagesTrimsToMaxMessages(t *testing.T) {
	history := make([]Message, 5)
	for index := range history {
		history[index] = TextMessage{Role: RoleUser, Content: "message"}
	}

	normalized := normalizeHistoryMessages(history, 3)
	if len(normalized) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(normalized))
	}
}

func TestNormalizeHistoryMessagesKeepsAllWhenWithinLimit(t *testing.T) {
	history := []Message{
		TextMessage{Role: RoleUser, Content: "one"},
		TextMessage{Role: RoleAssistant, Content: "two"},
	}

	normalized := normalizeHistoryMessages(history, 10)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(normalized))
	}
}

func TestNormalizeHistoryMessagesDoesNotTrimWhenMaxMessagesIsZero(t *testing.T) {
	history := make([]Message, 4)
	for index := range history {
		history[index] = TextMessage{Role: RoleUser, Content: "message"}
	}

	normalized := normalizeHistoryMessages(history, 0)
	if len(normalized) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(normalized))
	}
}

func TestAssignMessageIDsPreservesExistingIDs(t *testing.T) {
	messages := assignMessageIDs([]Message{
		TextMessage{ID: "msg-existing", Role: RoleUser, Content: "hello"},
		ToolCallMessage{ID: "msg-tool-call", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
		ToolResultMessage{ID: "msg-tool-result", ToolResult: ToolResult{ToolCallID: "call-1", ToolName: "read_file", Output: "ok"}},
	})

	textMessage, ok := messages[0].(TextMessage)
	if !ok || textMessage.ID != "msg-existing" {
		t.Fatalf("unexpected text message: %+v", messages[0])
	}
	toolCallMessage, ok := messages[1].(ToolCallMessage)
	if !ok || toolCallMessage.ID != "msg-tool-call" {
		t.Fatalf("unexpected tool call message: %+v", messages[1])
	}
	toolResultMessage, ok := messages[2].(ToolResultMessage)
	if !ok || toolResultMessage.ID != "msg-tool-result" {
		t.Fatalf("unexpected tool result message: %+v", messages[2])
	}
}

func TestAssignMessageIDsGeneratesMissingIDs(t *testing.T) {
	messages := assignMessageIDs([]Message{
		TextMessage{Role: RoleUser, Content: "hello"},
		&TextMessage{Role: RoleAssistant, Content: "hi"},
		ToolCallMessage{ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
		&ToolResultMessage{ToolResult: ToolResult{ToolCallID: "call-1", ToolName: "read_file", Output: "ok"}},
	})

	for index, message := range messages {
		id := messageID(message)
		if id == "" {
			t.Fatalf("expected generated id at index %d, got empty", index)
		}
		if !strings.HasPrefix(id, "msg-") {
			t.Fatalf("expected generated id prefix msg-, got %q", id)
		}
	}
}

func messageID(message Message) string {
	switch typedMessage := message.(type) {
	case TextMessage:
		return typedMessage.ID
	case *TextMessage:
		if typedMessage == nil {
			return ""
		}
		return typedMessage.ID
	case ToolCallMessage:
		return typedMessage.ID
	case *ToolCallMessage:
		if typedMessage == nil {
			return ""
		}
		return typedMessage.ID
	case ToolResultMessage:
		return typedMessage.ID
	case *ToolResultMessage:
		if typedMessage == nil {
			return ""
		}
		return typedMessage.ID
	default:
		return ""
	}
}

func TestNormalizeRunOptionsSetsDefaultMaxMessages(t *testing.T) {
	options := normalizeRunOptions(RunOptions{})
	if options.MaxMessages != DefaultMaxMessages {
		t.Fatalf("expected default max messages %d, got %d", DefaultMaxMessages, options.MaxMessages)
	}
}
