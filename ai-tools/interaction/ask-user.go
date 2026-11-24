// Package interaction adapts an injected user interface to a tool.
package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

func NewAskUserTool(ask protocol.AskUserFunc) (loopprot.Tool, error) {
	if ask == nil {
		return loopprot.Tool{}, fmt.Errorf("user input callback is required")
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.AskUser,
			Description: "Ask the user one question when needed to continue. Omit choices for a free-text answer.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1,"maxLength":1000},"choices":{"type":"array","minItems":2,"maxItems":8,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":160}}},"required":["question"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var question protocol.UserQuestion
			if err := utils.DecodeObject(arguments, &question); err != nil {
				return aiprot.ToolResult{}, err
			}
			if err := question.Validate(); err != nil {
				return aiprot.ToolResult{}, err
			}
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			answer, err := ask(ctx, question)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			if strings.TrimSpace(answer) == "" || (len(question.Choices) > 0 && !slices.Contains(question.Choices, answer)) {
				return aiprot.ToolResult{}, fmt.Errorf("user answer must be nonempty and match a choice when choices were supplied")
			}
			return aiprot.TextToolResult(answer), nil
		},
	}, nil
}
