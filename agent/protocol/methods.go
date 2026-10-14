package protocol

import (
	"errors"
	"fmt"
	"strings"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func (request Request) Validate() error {
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("agent model is required")
	}
	if err := validateHistory(request.History); err != nil {
		return fmt.Errorf("invalid agent history: %w", err)
	}
	if strings.TrimSpace(request.UserPrompt) == "" {
		for _, message := range request.History {
			if len(message.Parts) > 0 {
				return nil
			}
		}
		return errors.New("agent user prompt or non-empty history is required")
	}
	return nil
}

// A historical tool batch must be complete before another conversation turn.
// This prevents replaying orphaned results or continuing past unresolved calls.
func validateHistory(history []aiprot.Message) error {
	pending := make(map[string]string)
	for index, message := range history {
		if len(pending) > 0 && message.Role != aiprot.RoleTool {
			return fmt.Errorf("tool calls need results before message %d", index)
		}
		switch message.Role {
		case aiprot.RoleSystem, aiprot.RoleUser, aiprot.RoleAssistant:
		case aiprot.RoleTool:
			if len(message.Parts) == 0 {
				return fmt.Errorf("tool message %d has no results", index)
			}
		default:
			return fmt.Errorf("message %d has unsupported role %q", index, message.Role)
		}
		for _, part := range message.Parts {
			if message.Role == aiprot.RoleTool && part.Type != aiprot.ContentToolResult {
				return fmt.Errorf("tool message %d must contain only results", index)
			}
			switch part.Type {
			case aiprot.ContentToolCall:
				call := part.ToolCall
				if message.Role != aiprot.RoleAssistant || call == nil || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
					return fmt.Errorf("message %d has an invalid assistant tool call", index)
				}
				if _, exists := pending[call.ID]; exists {
					return fmt.Errorf("duplicate tool call %q", call.ID)
				}
				pending[call.ID] = call.Name
			case aiprot.ContentToolResult:
				result := part.ToolResult
				if message.Role != aiprot.RoleTool || result == nil {
					return fmt.Errorf("message %d has an invalid tool result", index)
				}
				name, exists := pending[result.ToolCallID]
				if !exists || name != result.ToolName {
					return fmt.Errorf("tool result %q does not match a pending call", result.ToolCallID)
				}
				delete(pending, result.ToolCallID)
			}
		}
	}
	if len(pending) > 0 {
		return errors.New("tool calls are missing results")
	}
	return nil
}

// LoopRequest preserves prompt text, omits an empty user prompt, and snapshots
// history and tool definitions before the caller's data enters the loop.
func (request Request) LoopRequest() loopprot.Request {
	result := loopprot.Request{Model: request.Model, Tools: request.Tools, Options: request.Options}
	if request.SystemPrompt != "" {
		result.Messages = append(result.Messages, aiprot.TextMessage(aiprot.RoleSystem, request.SystemPrompt))
	}
	result.Messages = append(result.Messages, request.History...)
	if strings.TrimSpace(request.UserPrompt) != "" {
		result.Messages = append(result.Messages, aiprot.TextMessage(aiprot.RoleUser, request.UserPrompt))
	}
	return result.Clone()
}
