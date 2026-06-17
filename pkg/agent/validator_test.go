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
			name:      "valid missing provider",
			mutate:    func(request *Request) { request.ModelConfig.Provider = "" },
			wantError: "",
		},
		{
			name:      "missing user prompt",
			mutate:    func(request *Request) { request.UserPrompt = "" },
			wantError: "user prompt is required",
		},
		{
			name:      "negative max steps",
			mutate:    func(request *Request) { request.Options.MaxSteps = -1 },
			wantError: "max steps must be greater than or equal to 0",
		},
		{
			name:      "negative max messages",
			mutate:    func(request *Request) { request.Options.MaxMessages = -1 },
			wantError: "max messages must be greater than or equal to 0",
		},
		{
			name: "missing tool name",
			mutate: func(request *Request) {
				request.Tools = []Tool{{Handler: ToolHandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "", nil })}}
			},
			wantError: "tool name is required",
		},
		{
			name: "missing tool handler",
			mutate: func(request *Request) {
				request.Tools = []Tool{{Name: "read_file"}}
			},
			wantError: "tool handler is required: read_file",
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
