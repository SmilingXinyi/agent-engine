package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	protocol "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

type streamChunk struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Created int64       `json:"created"`
	Usage   openAIUsage `json:"usage"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string         `json:"role"`
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			Reasoning        string         `json:"reasoning"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type toolCallBuilder struct {
	id        string
	name      strings.Builder
	arguments strings.Builder
}

type streamAccumulator struct {
	text         strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*toolCallBuilder
	providerName string
	finishReason protocol.FinishReason
	usage        protocol.Usage
	id           string
	model        string
	created      int64
}

func (provider *Provider) Stream(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
	if provider.apiType == APITypeResponsesAPI {
		return provider.streamResponses(ctx, request, sink)
	}
	payload, err := provider.buildRequest(request, true)
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	response, err := provider.send(ctx, payload)
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if err := validateHTTPResponse(response); err != nil {
		return protocol.ModelResponse{}, err
	}

	accumulator := streamAccumulator{
		toolCalls:    make(map[int]*toolCallBuilder),
		providerName: provider.name,
	}
	scanner := bufio.NewScanner(response.Body)
	maxLineBytes := provider.maxResponseBytes
	if maxLineBytes > 10*1024*1024 {
		maxLineBytes = 10 * 1024 * 1024
	}
	scanner.Buffer(make([]byte, 64*1024), int(maxLineBytes))
	receivedDone := false
	var totalBytes int64
	for scanner.Scan() {
		totalBytes += int64(len(scanner.Bytes()))
		if totalBytes > provider.maxResponseBytes {
			return accumulator.response(), fmt.Errorf("OpenAI stream response exceeds %d bytes", provider.maxResponseBytes)
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			receivedDone = true
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return accumulator.response(), fmt.Errorf("decode OpenAI stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return accumulator.response(), fmt.Errorf("OpenAI stream error: %s", chunk.Error.Message)
		}
		if err := accumulator.consume(ctx, chunk, sink); err != nil {
			return accumulator.response(), err
		}
	}
	if err := scanner.Err(); err != nil {
		return accumulator.response(), fmt.Errorf("read OpenAI stream: %w", err)
	}
	if !receivedDone {
		return accumulator.response(), fmt.Errorf("OpenAI stream ended without [DONE]")
	}
	return accumulator.response(), nil
}

func (accumulator *streamAccumulator) consume(ctx context.Context, chunk streamChunk, sink protocol.EventSink) error {
	if chunk.ID != "" {
		accumulator.id = chunk.ID
	}
	if chunk.Model != "" {
		accumulator.model = chunk.Model
	}
	if chunk.Created != 0 {
		accumulator.created = chunk.Created
	}
	if chunk.Usage.TotalTokens != 0 || chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
		accumulator.usage = convertUsage(chunk.Usage)
		if sink != nil {
			usage := accumulator.usage
			if err := sink.OnEvent(ctx, protocol.StreamEvent{Type: protocol.EventUsage, Usage: &usage}); err != nil {
				return fmt.Errorf("stream usage event: %w", err)
			}
		}
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		if choice.Delta.Content != "" {
			accumulator.text.WriteString(choice.Delta.Content)
			if sink != nil {
				if err := sink.OnEvent(ctx, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: choice.Delta.Content}); err != nil {
					return fmt.Errorf("stream text event: %w", err)
				}
			}
		}
		reasoning := choice.Delta.ReasoningContent
		if reasoning == "" {
			reasoning = choice.Delta.Reasoning
		}
		if reasoning != "" {
			accumulator.reasoning.WriteString(reasoning)
			if sink != nil {
				if err := sink.OnEvent(ctx, protocol.StreamEvent{Type: protocol.EventReasoningDelta, Delta: reasoning}); err != nil {
					return fmt.Errorf("stream reasoning event: %w", err)
				}
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			builder := accumulator.toolCalls[delta.Index]
			if builder == nil {
				builder = &toolCallBuilder{}
				accumulator.toolCalls[delta.Index] = builder
			}
			if delta.ID != "" {
				builder.id = delta.ID
			}
			builder.name.WriteString(delta.Function.Name)
			builder.arguments.WriteString(delta.Function.Arguments)
			if sink != nil {
				event := protocol.StreamEvent{
					Type: protocol.EventToolCallDelta,
					ToolCallDelta: &protocol.ToolCallDelta{
						Index: delta.Index, ID: delta.ID, Name: delta.Function.Name,
						ArgumentsDelta: delta.Function.Arguments,
					},
				}
				if err := sink.OnEvent(ctx, event); err != nil {
					return fmt.Errorf("stream tool call event: %w", err)
				}
			}
		}
		if choice.FinishReason != "" {
			accumulator.finishReason = mapFinishReason(choice.FinishReason)
		}
	}
	return nil
}

func (accumulator *streamAccumulator) response() protocol.ModelResponse {
	message := protocol.Message{Role: protocol.RoleAssistant}
	if accumulator.text.Len() > 0 {
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentText, Text: accumulator.text.String()})
	}
	if accumulator.reasoning.Len() > 0 {
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentReasoning, Text: accumulator.reasoning.String()})
	}
	indexes := make([]int, 0, len(accumulator.toolCalls))
	for index := range accumulator.toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		builder := accumulator.toolCalls[index]
		call := protocol.ToolCall{
			ID: builder.id, Name: builder.name.String(), Arguments: json.RawMessage(builder.arguments.String()),
		}
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentToolCall, ToolCall: &call})
	}
	finishReason := accumulator.finishReason
	if finishReason == "" {
		if len(accumulator.toolCalls) > 0 {
			finishReason = protocol.FinishToolCalls
		} else {
			finishReason = protocol.FinishStop
		}
	}
	return protocol.ModelResponse{
		Message:      message,
		FinishReason: finishReason,
		Usage:        accumulator.usage,
		Metadata: responseMetadata(
			accumulator.providerName, accumulator.id, accumulator.model, accumulator.created,
		),
	}
}
