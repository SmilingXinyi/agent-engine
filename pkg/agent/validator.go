package agent

import (
	"context"
	"fmt"
	"strings"
)

type defaultRequestValidator struct{}

// ValidateRequest checks required request fields before running the agent.
func (defaultRequestValidator) ValidateRequest(ctx context.Context, request Request) error {
	_ = ctx
	if strings.TrimSpace(request.ModelConfig.APIKey) == "" {
		return fmt.Errorf("llm api key is required")
	}
	if strings.TrimSpace(request.ModelConfig.Name) == "" {
		return fmt.Errorf("llm model is required")
	}
	if strings.TrimSpace(request.UserPrompt) == "" {
		return fmt.Errorf("user prompt is required")
	}
	if request.Options.MaxSteps < 0 {
		return fmt.Errorf("max steps must be greater than or equal to 0")
	}
	if request.Options.MaxMessages < 0 {
		return fmt.Errorf("max messages must be greater than or equal to 0")
	}
	seenTools := make(map[string]struct{}, len(request.Tools))
	for index, tool := range request.Tools {
		toolName := strings.TrimSpace(tool.Name)
		if toolName == "" {
			return fmt.Errorf("tool name is required at index %d", index)
		}
		if tool.Handler == nil {
			return fmt.Errorf("tool handler is required: %s", toolName)
		}
		if _, ok := seenTools[toolName]; ok {
			return fmt.Errorf("duplicate tool name: %s", toolName)
		}
		seenTools[toolName] = struct{}{}
	}
	return nil
}
