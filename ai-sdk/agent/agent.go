package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/tool"
)

// Agent runs loops of reasoning, tool calls, and text generation.
type Agent struct {
	model        model.Model
	systemPrompt string
	registry     tool.Registry
	maxSteps     int
}

// Option configures an Agent.
type Option func(*Agent)

// WithSystemPrompt sets the system prompt.
func WithSystemPrompt(prompt string) Option {
	return func(a *Agent) {
		a.systemPrompt = prompt
	}
}

// WithToolRegistry sets the tool registry.
func WithToolRegistry(reg tool.Registry) Option {
	return func(a *Agent) {
		a.registry = reg
	}
}

// WithTools adds tools to the agent.
func WithTools(tools ...tool.Tool) Option {
	return func(a *Agent) {
		for _, t := range tools {
			_ = a.registry.Register(t)
		}
	}
}

// WithMaxSteps sets the maximum reasoning steps for tool execution loops.
func WithMaxSteps(steps int) Option {
	return func(a *Agent) {
		if steps > 0 {
			a.maxSteps = steps
		}
	}
}

// New creates a new Agent instance.
func New(m model.Model, opts ...Option) *Agent {
	a := &Agent{
		model:    m,
		registry: tool.NewRegistry(),
		maxSteps: 10,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// StepCallback is invoked after each LLM call or tool execution in the loop.
type StepCallback func(step int, resp *model.GenerateResponse)

// Run executes the agent loop until completion or max steps is reached.
func (a *Agent) Run(ctx context.Context, messages []model.Message, opts ...model.RequestOptions) (*model.GenerateResponse, error) {
	var options model.RequestOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	currentMessages := make([]model.Message, len(messages))
	copy(currentMessages, messages)

	tools := a.registry.Definitions()

	for step := 0; step < a.maxSteps; step++ {
		req := model.GenerateRequest{
			SystemPrompt: a.systemPrompt,
			Messages:     currentMessages,
			Tools:        tools,
			Options:      options,
		}

		resp, err := a.model.Generate(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("agent step %d generation failed: %w", step, err)
		}

		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}

		assistantMsg := model.Message{
			Role:    model.RoleAssistant,
			Content: resp.Text,
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.Parts = append(assistantMsg.Parts, model.ContentPart{
				Type:       model.ContentTypeToolCall,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolInput:  tc.Arguments,
			})
		}
		currentMessages = append(currentMessages, assistantMsg)

		for _, tc := range resp.ToolCalls {
			t, ok := a.registry.Get(tc.Name)
			var toolOutput string
			if !ok {
				toolOutput = fmt.Sprintf("Error: tool %q not found", tc.Name)
			} else {
				out, err := t.Execute(ctx, json.RawMessage(tc.Arguments))
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %s", err.Error())
				} else {
					switch v := out.(type) {
					case string:
						toolOutput = v
					default:
						b, _ := json.Marshal(v)
						toolOutput = string(b)
					}
				}
			}

			currentMessages = append(currentMessages, model.ToolResultMessage(tc.ID, tc.Name, toolOutput))
		}
	}

	return nil, fmt.Errorf("agent reached maximum execution steps (%d)", a.maxSteps)
}

// Stream executes the agent loop while streaming responses to the handler.
func (a *Agent) Stream(ctx context.Context, messages []model.Message, handler model.StreamHandler, opts ...model.RequestOptions) (*model.GenerateResponse, error) {
	var options model.RequestOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	currentMessages := make([]model.Message, len(messages))
	copy(currentMessages, messages)
	tools := a.registry.Definitions()

	for step := 0; step < a.maxSteps; step++ {
		req := model.GenerateRequest{
			SystemPrompt: a.systemPrompt,
			Messages:     currentMessages,
			Tools:        tools,
			Options:      options,
		}

		resp, err := a.model.Stream(ctx, req, handler)
		if err != nil {
			return nil, fmt.Errorf("agent stream step %d failed: %w", step, err)
		}

		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}

		assistantMsg := model.Message{
			Role:    model.RoleAssistant,
			Content: resp.Text,
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.Parts = append(assistantMsg.Parts, model.ContentPart{
				Type:       model.ContentTypeToolCall,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolInput:  tc.Arguments,
			})
		}
		currentMessages = append(currentMessages, assistantMsg)

		for _, tc := range resp.ToolCalls {
			t, ok := a.registry.Get(tc.Name)
			var toolOutput string
			if !ok {
				toolOutput = fmt.Sprintf("Error: tool %q not found", tc.Name)
			} else {
				out, err := t.Execute(ctx, json.RawMessage(tc.Arguments))
				if err != nil {
					toolOutput = fmt.Sprintf("Error: %s", err.Error())
				} else {
					switch v := out.(type) {
					case string:
						toolOutput = v
					default:
						b, _ := json.Marshal(v)
						toolOutput = string(b)
					}
				}
			}

			currentMessages = append(currentMessages, model.ToolResultMessage(tc.ID, tc.Name, toolOutput))
		}
	}

	return nil, fmt.Errorf("agent reached maximum execution steps (%d) in stream mode", a.maxSteps)
}
