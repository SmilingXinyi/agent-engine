package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// Stream returns partial content with Complete=false on any error. A successful
// call emits EventFinish once, after all deltas and usage have been delivered.
func (provider *Provider) Stream(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (result protocol.ModelResponse, err error) {
	payload, err := provider.buildRequest(request, true)
	if err != nil {
		return result, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return result, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	response, info, err := provider.send(ctx, encoded, true)
	result.CallInfo = info
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if err = provider.validateHTTPResponse(response, info); err != nil {
		return result, err
	}
	if provider.apiType == APITypeResponsesAPI {
		result, err = provider.streamResponses(ctx, response.Body, sink)
	} else {
		result, err = provider.streamChat(ctx, response.Body, sink)
	}
	result.CallInfo = info
	if err != nil {
		result.Complete = false
		return result, provider.failure(err, protocol.ErrorTransport, info)
	}
	if result.FinishReason == protocol.FinishError {
		return result, provider.failure(fmt.Errorf("upstream reported a stream failure"), protocol.ErrorUpstream, info)
	}
	if err = emitEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventFinish, FinishReason: result.FinishReason}); err != nil {
		result.Complete = false
		return result, provider.failure(err, protocol.ErrorSink, info)
	}
	result.Complete = true
	return result, nil
}

func (provider *Provider) streamChat(ctx context.Context, body io.Reader, sink protocol.EventSink) (protocol.ModelResponse, error) {
	accumulator := streamAccumulator{toolCalls: make(map[int]*toolCallBuilder), providerName: provider.name}
	receivedDone := false
	err := provider.readSSE(ctx, body, func(event RawEvent) error {
		if len(event.Data) == 0 {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			receivedDone = true
			return errSSEComplete
		}
		var chunk streamChunk
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			return &protocol.Error{Code: protocol.ErrorDecode, Message: err.Error(), Cause: err}
		}
		if chunk.Error != nil {
			return &protocol.Error{Code: protocol.ErrorUpstream, Message: chunk.Error.Message}
		}
		return accumulator.consume(ctx, chunk, sink)
	})
	result := accumulator.response()
	if err != nil {
		return result, err
	}
	if !receivedDone && (provider.requireDone || accumulator.finishReason == "") {
		return result, &protocol.Error{Code: protocol.ErrorStreamIncomplete, Message: "stream ended without [DONE] or a finish reason"}
	}
	if result.FinishReason == "" {
		result.FinishReason = protocol.FinishStop
	}
	return result, nil
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
			if err := emitEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventUsage, Usage: &usage}); err != nil {
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
				if err := emitEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: choice.Delta.Content}); err != nil {
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
				if err := emitEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventReasoningDelta, Delta: reasoning}); err != nil {
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

	return protocol.ModelResponse{
		Message:      message,
		FinishReason: finishReason,
		Usage:        accumulator.usage,
		Metadata: responseMetadata(
			accumulator.providerName, accumulator.id, accumulator.model, accumulator.created,
		),
	}
}
