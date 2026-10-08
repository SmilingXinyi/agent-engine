// Package aisdk provides core abstractions and utilities for building AI agent applications.
package aisdk

import (
	"context"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/agent"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/provider"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/schema"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/tool"
)

// Re-export common types for convenient root-package usage.
type (
	Model          = model.Model
	Message        = model.Message
	Role           = model.Role
	ContentPart    = model.ContentPart
	ToolCall       = model.ToolCall
	ToolDefinition = model.ToolDefinition
	StreamChunk    = model.StreamChunk
	StreamHandler  = model.StreamHandler
	RequestOptions = model.RequestOptions

	Tool         = tool.Tool
	ToolRegistry = tool.Registry

	Agent = agent.Agent

	Provider = provider.Provider
)

// Helper functions for building messages.
var (
	UserMessage      = model.UserMessage
	AssistantMessage = model.AssistantMessage
	SystemMessage    = model.SystemMessage
	ToolResultMessage = model.ToolResultMessage
)

// NewAgent creates a new Agent instance.
func NewAgent(m model.Model, opts ...agent.Option) *agent.Agent {
	return agent.New(m, opts...)
}

// NewTool creates a new Tool with the specified parameters and handler.
func NewTool(name, description string, paramsStruct any, handler tool.Handler) (*tool.BaseTool, error) {
	params, err := schema.GenerateJSONSchema(paramsStruct)
	if err != nil {
		return nil, err
	}
	return tool.New(name, description, params, handler), nil
}

// GenerateText is a simple entrypoint for non-streaming text generation.
func GenerateText(ctx context.Context, m model.Model, promptText string, opts ...model.RequestOptions) (string, error) {
	var opt model.RequestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	resp, err := m.Generate(ctx, model.GenerateRequest{
		Messages: []model.Message{model.UserMessage(promptText)},
		Options:  opt,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// StreamText is a simple entrypoint for streaming text generation.
func StreamText(ctx context.Context, m model.Model, promptText string, onChunk func(chunk string) error, opts ...model.RequestOptions) (*model.GenerateResponse, error) {
	var opt model.RequestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	return m.Stream(ctx, model.GenerateRequest{
		Messages: []model.Message{model.UserMessage(promptText)},
		Options:  opt,
	}, func(ctx context.Context, chunk model.StreamChunk) error {
		if chunk.Type == model.ChunkTypeText && onChunk != nil {
			return onChunk(chunk.Text)
		}
		return nil
	})
}
