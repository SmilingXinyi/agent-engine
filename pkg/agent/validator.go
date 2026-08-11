package agent

import (
	"context"
	"fmt"
	"strings"
)

type defaultRequestValidator struct{}

// ValidateRequest checks required request fields before running the agent.
func (defaultRequestValidator) ValidateRequest(ctx context.Context, request Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(request.ModelConfig.APIKey) == "" {
		return fmt.Errorf("llm api key is required")
	}
	if strings.TrimSpace(request.ModelConfig.Name) == "" {
		return fmt.Errorf("llm model is required")
	}
	// A continuation turn (tool_results only) has no new user/system prompt but
	// carries History with the prior conversation. Require at least one of:
	// system prompt, user prompt, or a non-empty history.
	if strings.TrimSpace(request.SystemPrompt) == "" && strings.TrimSpace(request.UserPrompt) == "" && len(request.History) == 0 {
		return fmt.Errorf("system prompt or user prompt is required")
	}
	if request.Options.MaxSteps < 0 {
		return fmt.Errorf("max steps must be greater than or equal to 0")
	}
	seenTools := make(map[string]struct{}, len(request.Tools))
	for index, tool := range request.Tools {
		toolName := strings.TrimSpace(tool.Name)
		if toolName == "" {
			return fmt.Errorf("tool name is required at index %d", index)
		}
		if toolName != tool.Name {
			return fmt.Errorf("tool name must not contain surrounding whitespace at index %d", index)
		}
		// Tools with a nil Handler are external stubs: the engine advertises them
		// to the LLM but will not execute them. They are valid.
		if _, ok := seenTools[toolName]; ok {
			return fmt.Errorf("duplicate tool name: %s", toolName)
		}
		seenTools[toolName] = struct{}{}
	}
	return nil
}
