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

type responsesStreamEvent struct {
	Type        string                `json:"type"`
	Delta       string                `json:"delta"`
	Arguments   string                `json:"arguments"`
	OutputIndex int                   `json:"output_index"`
	ItemID      string                `json:"item_id"`
	Item        responsesOutputItem   `json:"item"`
	Response    responsesResponse     `json:"response"`
	Error       *responsesStreamError `json:"error,omitempty"`
}

type responsesStreamError struct {
	Message string `json:"message"`
}

type responsesToolCallBuilder struct {
	id        string
	callID    string
	name      string
	arguments strings.Builder
}

type responsesStreamAccumulator struct {
	text         strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*responsesToolCallBuilder
	indexesByID  map[string]int
	providerName string
	finishReason protocol.FinishReason
	usage        protocol.Usage
	id           string
	model        string
	created      int64
	status       string
	incomplete   string
	completed    bool
}

func (provider *Provider) streamResponses(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
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

	accumulator := responsesStreamAccumulator{
		toolCalls:    make(map[int]*responsesToolCallBuilder),
		indexesByID:  make(map[string]int),
		providerName: provider.name,
	}
	scanner := bufio.NewScanner(response.Body)
	maxLineBytes := provider.maxResponseBytes
	if maxLineBytes > 10*1024*1024 {
		maxLineBytes = 10 * 1024 * 1024
	}
	scanner.Buffer(make([]byte, 64*1024), int(maxLineBytes))
	var eventName string
	var data strings.Builder
	var totalBytes int64
	flush := func() error {
		if data.Len() == 0 {
			eventName = ""
			return nil
		}
		payload := strings.TrimSpace(data.String())
		data.Reset()
		name := eventName
		eventName = ""
		if payload == "[DONE]" {
			accumulator.completed = true
			return nil
		}
		var event responsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("decode OpenAI Responses stream event: %w", err)
		}
		if name == "" {
			name = event.Type
		}
		if err := accumulator.consume(ctx, name, event, sink); err != nil {
			return err
		}
		return nil
	}
	for scanner.Scan() {
		totalBytes += int64(len(scanner.Bytes()))
		if totalBytes > provider.maxResponseBytes {
			return accumulator.response(), fmt.Errorf("OpenAI Responses stream response exceeds %d bytes", provider.maxResponseBytes)
		}
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return accumulator.response(), err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return accumulator.response(), fmt.Errorf("read OpenAI Responses stream: %w", err)
	}
	if err := flush(); err != nil {
		return accumulator.response(), err
	}
	if !accumulator.completed {
		return accumulator.response(), fmt.Errorf("OpenAI Responses stream ended without response.completed")
	}
	return accumulator.response(), nil
}

func (accumulator *responsesStreamAccumulator) consume(ctx context.Context, eventName string, event responsesStreamEvent, sink protocol.EventSink) error {
	switch eventName {
	case "response.output_text.delta":
		if event.Delta == "" {
			return nil
		}
		accumulator.text.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: event.Delta}, "text")
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.reasoning_content.delta":
		if event.Delta == "" {
			return nil
		}
		accumulator.reasoning.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventReasoningDelta, Delta: event.Delta}, "reasoning")
	case "response.function_call_arguments.delta":
		index := accumulator.toolCallIndex(event.OutputIndex, event.ItemID)
		builder := accumulator.toolCalls[index]
		if builder == nil {
			builder = &responsesToolCallBuilder{id: event.ItemID}
			accumulator.toolCalls[index] = builder
		}
		builder.arguments.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
			Index: index, ID: builder.id, Name: builder.name, ArgumentsDelta: event.Delta,
		}}, "tool call")
	case "response.function_call_arguments.done":
		index := accumulator.toolCallIndex(event.OutputIndex, event.ItemID)
		builder := accumulator.toolCalls[index]
		if builder == nil {
			builder = &responsesToolCallBuilder{id: event.ItemID}
			accumulator.toolCalls[index] = builder
		}
		return accumulator.setArguments(ctx, sink, index, builder, event.Arguments)
	case "response.output_item.added":
		if event.Item.Type == "function_call" {
			index := accumulator.toolCallIndex(event.OutputIndex, event.Item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: event.Item.ID}
				accumulator.toolCalls[index] = builder
			}
			builder.callID, builder.name = event.Item.CallID, event.Item.Name
			return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
				Index: index, ID: builder.callID, Name: builder.name,
			}}, "tool call")
		}
	case "response.output_item.done":
		if event.Item.Type == "function_call" {
			index := accumulator.toolCallIndex(event.OutputIndex, event.Item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: event.Item.ID}
				accumulator.toolCalls[index] = builder
			}
			if builder.callID == "" {
				builder.callID = event.Item.CallID
			}
			if builder.name == "" {
				builder.name = event.Item.Name
			}
			return accumulator.setArguments(ctx, sink, index, builder, event.Item.Arguments)
		}
	case "response.completed":
		accumulator.completed = true
		return accumulator.mergeResponse(ctx, sink, event.Response)
	case "response.incomplete":
		accumulator.completed = true
		return accumulator.mergeResponse(ctx, sink, event.Response)
	case "response.failed", "error":
		if event.Error != nil && event.Error.Message != "" {
			return fmt.Errorf("OpenAI Responses stream error: %s", event.Error.Message)
		}
		return fmt.Errorf("OpenAI Responses stream failed")
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) mergeResponse(ctx context.Context, sink protocol.EventSink, response responsesResponse) error {
	if response.ID != "" {
		accumulator.id = response.ID
	}
	if response.Model != "" {
		accumulator.model = response.Model
	}
	if response.CreatedAt != 0 {
		accumulator.created = response.CreatedAt
	}
	if response.Status != "" {
		accumulator.status = response.Status
	}
	accumulator.incomplete = response.IncompleteDetail.Reason
	hasUsage := response.Usage.TotalTokens != 0 || response.Usage.InputTokens != 0 || response.Usage.OutputTokens != 0
	if hasUsage {
		accumulator.usage = convertResponsesUsage(response.Usage)
	}
	if accumulator.text.Len() == 0 && response.OutputText != "" {
		accumulator.text.WriteString(response.OutputText)
		if err := emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: response.OutputText}, "text"); err != nil {
			return err
		}
	}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			if accumulator.text.Len() == 0 {
				for _, part := range item.Content {
					if part.Type == "output_text" || part.Type == "text" || part.Type == "refusal" {
						if part.Text != "" {
							accumulator.text.WriteString(part.Text)
							if err := emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: part.Text}, "text"); err != nil {
								return err
							}
						}
					}
				}
			}
		case "reasoning":
			if accumulator.reasoning.Len() == 0 {
				for _, part := range item.Summary {
					if part.Text != "" {
						accumulator.reasoning.WriteString(part.Text)
						if err := emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventReasoningDelta, Delta: part.Text}, "reasoning"); err != nil {
							return err
						}
					}
				}
			}
		case "function_call":
			index := accumulator.toolCallIndex(0, item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: item.ID}
				accumulator.toolCalls[index] = builder
			}
			if builder.callID == "" {
				builder.callID = item.CallID
			}
			if builder.name == "" {
				builder.name = item.Name
			}
			if err := accumulator.setArguments(ctx, sink, index, builder, item.Arguments); err != nil {
				return err
			}
		}
	}
	if hasUsage {
		usage := accumulator.usage
		if err := emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventUsage, Usage: &usage}, "usage"); err != nil {
			return err
		}
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) setArguments(ctx context.Context, sink protocol.EventSink, index int, builder *responsesToolCallBuilder, final string) error {
	if final == "" {
		return nil
	}
	current := builder.arguments.String()
	if strings.HasPrefix(final, current) {
		suffix := strings.TrimPrefix(final, current)
		if suffix != "" {
			builder.arguments.WriteString(suffix)
			return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
				Index: index, ID: builder.callID, Name: builder.name, ArgumentsDelta: suffix,
			}}, "tool call")
		}
	}
	if current == "" {
		builder.arguments.WriteString(final)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
			Index: index, ID: builder.callID, Name: builder.name, ArgumentsDelta: final,
		}}, "tool call")
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) toolCallIndex(outputIndex int, itemID string) int {
	if itemID != "" {
		if index, ok := accumulator.indexesByID[itemID]; ok {
			return index
		}
	}
	if outputIndex >= 0 {
		if itemID != "" {
			accumulator.indexesByID[itemID] = outputIndex
		}
		return outputIndex
	}
	index := len(accumulator.toolCalls)
	if itemID != "" {
		accumulator.indexesByID[itemID] = index
	}
	return index
}

func (accumulator *responsesStreamAccumulator) response() protocol.ModelResponse {
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
		call := protocol.ToolCall{ID: builder.callID, Name: builder.name, Arguments: json.RawMessage(builder.arguments.String())}
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentToolCall, ToolCall: &call})
	}
	finishReason := accumulator.finishReason
	if finishReason == "" {
		finishReason = responsesFinishReason(accumulator.status, accumulator.incomplete, len(accumulator.toolCalls) > 0)
		if finishReason == "" && accumulator.completed {
			finishReason = protocol.FinishStop
		}
	}
	return protocol.ModelResponse{
		Message: message, FinishReason: finishReason, Usage: accumulator.usage,
		Metadata: responseMetadata(accumulator.providerName, accumulator.id, accumulator.model, accumulator.created),
	}
}

func emitResponsesEvent(ctx context.Context, sink protocol.EventSink, event protocol.StreamEvent, kind string) error {
	if sink == nil {
		return nil
	}
	if err := sink.OnEvent(ctx, event); err != nil {
		return fmt.Errorf("stream %s event: %w", kind, err)
	}
	return nil
}
