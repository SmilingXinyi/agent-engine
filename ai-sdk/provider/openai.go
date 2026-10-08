package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
)

// OpenAIProvider implements Provider for OpenAI and OpenAI-compatible endpoints.
type OpenAIProvider struct {
	config Config
	client *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider instance.
func NewOpenAIProvider(config Config) *OpenAIProvider {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	return &OpenAIProvider{
		config: config,
		client: http.DefaultClient,
	}
}

func (p *OpenAIProvider) ID() string {
	return "openai"
}

func (p *OpenAIProvider) Model(name string) (model.Model, error) {
	if name == "" {
		return nil, fmt.Errorf("model name cannot be empty")
	}
	return &openaiModel{
		provider: p,
		name:     name,
	}, nil
}

type openaiModel struct {
	provider *OpenAIProvider
	name     string
}

func (m *openaiModel) Name() string {
	return m.name
}

type openAIChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCallItem `json:"tool_calls,omitempty"`
}

type openAIToolCallItem struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIFunctionCallItem `json:"function"`
}

type openAIFunctionCallItem struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string                 `json:"type"`
	Function openAIFunctionMetadata `json:"function"`
}

type openAIFunctionMetadata struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Tools       []openAITool        `json:"tools,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
	TopP        *float64            `json:"top_p,omitempty"`
	Stop        []string            `json:"stop,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

func (m *openaiModel) buildPayload(req model.GenerateRequest, stream bool) openAIChatRequest {
	payload := openAIChatRequest{
		Model:       m.name,
		Temperature: req.Options.Temperature,
		MaxTokens:   req.Options.MaxTokens,
		TopP:        req.Options.TopP,
		Stop:        req.Options.Stop,
		Stream:      stream,
	}

	if req.SystemPrompt != "" {
		payload.Messages = append(payload.Messages, openAIChatMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		cm := openAIChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}

		for _, part := range msg.Parts {
			switch part.Type {
			case model.ContentTypeText:
				if cm.Content == "" {
					cm.Content = part.Text
				}
			case model.ContentTypeToolCall:
				cm.ToolCalls = append(cm.ToolCalls, openAIToolCallItem{
					ID:   part.ToolCallID,
					Type: "function",
					Function: openAIFunctionCallItem{
						Name:      part.ToolName,
						Arguments: part.ToolInput,
					},
				})
			case model.ContentTypeToolResult:
				cm.Role = "tool"
				cm.ToolCallID = part.ToolCallID
				cm.Content = part.ToolOutput
			}
		}

		payload.Messages = append(payload.Messages, cm)
	}

	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, openAITool{
			Type: "function",
			Function: openAIFunctionMetadata{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	return payload
}

func (m *openaiModel) Generate(ctx context.Context, req model.GenerateRequest) (*model.GenerateResponse, error) {
	payload := m.buildPayload(req, false)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", m.provider.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if m.provider.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.provider.config.APIKey)
	}

	httpResp, err := m.provider.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("openai error (%d): %s", httpResp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(httpResp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	res := &model.GenerateResponse{
		Usage: model.Usage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
	}

	if len(chatResp.Choices) > 0 {
		choice := chatResp.Choices[0]
		res.Text = choice.Message.Content
		res.FinishReason = choice.FinishReason

		for _, tc := range choice.Message.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, model.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return res, nil
}

func (m *openaiModel) Stream(ctx context.Context, req model.GenerateRequest, handler model.StreamHandler) (*model.GenerateResponse, error) {
	payload := m.buildPayload(req, true)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", m.provider.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if m.provider.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.provider.config.APIKey)
	}

	httpResp, err := m.provider.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("openai stream error (%d): %s", httpResp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(httpResp.Body)
	finalResp := &model.GenerateResponse{}
	var textBuilder strings.Builder

	type streamDelta struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk streamDelta
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.FinishReason != "" {
				finalResp.FinishReason = choice.FinishReason
			}

			if choice.Delta.Content != "" {
				textBuilder.WriteString(choice.Delta.Content)
				if err := handler(ctx, model.StreamChunk{
					Type: model.ChunkTypeText,
					Text: choice.Delta.Content,
				}); err != nil {
					return nil, err
				}
			}

			for _, tc := range choice.Delta.ToolCalls {
				tcCopy := model.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
				if err := handler(ctx, model.StreamChunk{
					Type:     model.ChunkTypeToolCall,
					ToolCall: &tcCopy,
				}); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	finalResp.Text = textBuilder.String()
	_ = handler(ctx, model.StreamChunk{
		Type:         model.ChunkTypeFinish,
		FinishReason: finalResp.FinishReason,
	})

	return finalResp, nil
}
