package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultRequestValidatorValidateRequest(t *testing.T) {
	baseRequest := Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
	}
	tests := []struct {
		name      string
		mutate    func(*Request)
		wantError string
	}{
		{
			name:      "missing api key",
			mutate:    func(request *Request) { request.ModelConfig.APIKey = "" },
			wantError: "llm api key is required",
		},
		{
			name:      "missing model",
			mutate:    func(request *Request) { request.ModelConfig.Name = "" },
			wantError: "llm model is required",
		},
		{
			name:      "valid missing base url",
			mutate:    func(request *Request) { request.ModelConfig.BaseURL = "" },
			wantError: "",
		},
		{
			name: "missing system prompt and user prompt",
			mutate: func(request *Request) {
				request.SystemPrompt = ""
				request.UserPrompt = ""
			},
			wantError: "system prompt or user prompt is required",
		},
		{
			name: "system prompt without user prompt",
			mutate: func(request *Request) {
				request.SystemPrompt = "system"
				request.UserPrompt = ""
			},
			wantError: "",
		},
		{
			name:      "negative max steps",
			mutate:    func(request *Request) { request.Options.MaxSteps = -1 },
			wantError: "max steps must be greater than or equal to 0",
		},
		{
			name: "missing tool name",
			mutate: func(request *Request) {
				request.Tools = []Tool{{Handler: ToolHandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "", nil })}}
			},
			wantError: "tool name is required",
		},
		{
			name: "nil handler is valid external stub",
			mutate: func(request *Request) {
				request.Tools = []Tool{{Name: "read_file"}}
			},
			wantError: "",
		},
		{
			name: "tool name with surrounding whitespace",
			mutate: func(request *Request) {
				request.Tools = []Tool{{Name: " read_file"}}
			},
			wantError: "surrounding whitespace",
		},
		{
			name: "duplicate tool name",
			mutate: func(request *Request) {
				handler := ToolHandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "", nil })
				request.Tools = []Tool{{Name: "read_file", Handler: handler}, {Name: "read_file", Handler: handler}}
			},
			wantError: "duplicate tool name: read_file",
		},
		{
			name:      "valid zero max steps",
			mutate:    func(request *Request) { request.Options.MaxSteps = 0 },
			wantError: "",
		},
	}
	validator := defaultRequestValidator{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := baseRequest
			if test.mutate != nil {
				test.mutate(&request)
			}
			err := validator.ValidateRequest(context.Background(), request)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
		})
	}
}

func TestDefaultRequestValidatorReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (defaultRequestValidator{}).ValidateRequest(ctx, Request{})
	if err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
