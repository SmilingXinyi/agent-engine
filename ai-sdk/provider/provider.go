package provider

import (
	"context"
	"fmt"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
)

// Config holds common LLM provider connection settings.
type Config struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

// Provider represents a model provider service (such as OpenAI, Anthropic, Mock).
type Provider interface {
	// ID returns the identifier of the provider.
	ID() string
	// Model returns a Model instance for the specified model name.
	Model(name string) (model.Model, error)
}

// MockProvider is an in-memory implementation of Provider useful for testing and demos.
type MockProvider struct {
	responses map[string]*model.GenerateResponse
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		responses: make(map[string]*model.GenerateResponse),
	}
}

// RegisterResponse configures a canned response for a model name.
func (p *MockProvider) RegisterResponse(modelName string, resp *model.GenerateResponse) {
	p.responses[modelName] = resp
}

func (p *MockProvider) ID() string {
	return "mock"
}

func (p *MockProvider) Model(name string) (model.Model, error) {
	resp, ok := p.responses[name]
	if !ok {
		resp = &model.GenerateResponse{
			Text: fmt.Sprintf("Mock response from %s", name),
		}
	}
	return &mockModel{name: name, response: resp}, nil
}

type mockModel struct {
	name     string
	response *model.GenerateResponse
}

func (m *mockModel) Name() string {
	return m.name
}

func (m *mockModel) Generate(ctx context.Context, req model.GenerateRequest) (*model.GenerateResponse, error) {
	return m.response, nil
}

func (m *mockModel) Stream(ctx context.Context, req model.GenerateRequest, handler model.StreamHandler) (*model.GenerateResponse, error) {
	if m.response.Reasoning != "" {
		if err := handler(ctx, model.StreamChunk{
			Type:      model.ChunkTypeReasoning,
			Reasoning: m.response.Reasoning,
		}); err != nil {
			return nil, err
		}
	}

	if m.response.Text != "" {
		if err := handler(ctx, model.StreamChunk{
			Type: model.ChunkTypeText,
			Text: m.response.Text,
		}); err != nil {
			return nil, err
		}
	}

	for _, tc := range m.response.ToolCalls {
		tcCopy := tc
		if err := handler(ctx, model.StreamChunk{
			Type:     model.ChunkTypeToolCall,
			ToolCall: &tcCopy,
		}); err != nil {
			return nil, err
		}
	}

	_ = handler(ctx, model.StreamChunk{
		Type:         model.ChunkTypeFinish,
		FinishReason: m.response.FinishReason,
		Usage:        &m.response.Usage,
	})

	return m.response, nil
}
