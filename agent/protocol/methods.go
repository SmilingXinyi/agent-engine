package protocol

import (
	"errors"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"strings"
)

func (request Request) Validate() error {
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("agent model is required")
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
