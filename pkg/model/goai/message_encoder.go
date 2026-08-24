package goai

import (
	"fmt"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
)

func encodeMessageToProvider(message agent.Message) (provider.Message, bool, error) {
	switch typedMessage := message.(type) {
	case agent.TextMessage:
		return encodeTextMessage(typedMessage)
	case *agent.TextMessage:
		if typedMessage == nil {
			return provider.Message{}, false, fmt.Errorf("nil text message")
		}
		return encodeTextMessage(*typedMessage)
	case agent.ToolCallMessage:
		return encodeToolCallMessage(typedMessage), true, nil
	case *agent.ToolCallMessage:
		if typedMessage == nil {
			return provider.Message{}, false, fmt.Errorf("nil tool call message")
		}
		return encodeToolCallMessage(*typedMessage), true, nil
	case agent.ToolResultMessage:
		return encodeToolResultMessage(typedMessage), true, nil
	case *agent.ToolResultMessage:
		if typedMessage == nil {
			return provider.Message{}, false, fmt.Errorf("nil tool result message")
		}
		return encodeToolResultMessage(*typedMessage), true, nil
	default:
		return provider.Message{}, false, fmt.Errorf("unsupported message type")
	}
}

func encodeTextMessage(message agent.TextMessage) (provider.Message, bool, error) {
	switch message.Role {
	case agent.RoleUser:
		return goai.UserMessage(message.Content), true, nil
	case agent.RoleAssistant:
		return goai.AssistantMessage(message.Content), true, nil
	case agent.RoleSystem:
		return provider.Message{}, false, nil
	case agent.RoleToolCall, agent.RoleTool:
		return provider.Message{}, false, nil
	default:
		return provider.Message{}, false, fmt.Errorf("unsupported text message role: %s", message.Role)
	}
}

func encodeToolCallMessage(message agent.ToolCallMessage) provider.Message {
	parts := make([]provider.Part, 0, len(message.ToolCalls))
	for _, toolCall := range message.ToolCalls {
		parts = append(parts, provider.Part{
			Type:       provider.PartToolCall,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			ToolInput:  copyRawMessage(toolCall.Input),
		})
	}
	return provider.Message{Role: provider.RoleAssistant, Content: parts}
}

func encodeToolResultMessage(message agent.ToolResultMessage) provider.Message {
	return goai.ToolMessage(
		message.ToolResult.ToolCallID,
		message.ToolResult.ToolName,
		message.ToolResult.Output,
	)
}
