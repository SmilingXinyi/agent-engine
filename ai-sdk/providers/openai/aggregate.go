package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/utils"
)

// Accumulator rebuilds one chat.completion object from Chat Completions SSE
// data payloads. It does not read a stream. Pass each Chunk().Data to Add.
// [DONE] and empty payloads are ignored. A non-empty finish_reason marks the
// accumulator finished; later usage payloads are still applied.
//
// Only choices[0] is read. Assembled fields are id, model, created, role,
// content, reasoning_content, finish_reason, and the full usage object.
// tool_calls are left untouched.
type Accumulator struct {
	id           string
	model        string
	created      any
	hasCreated   bool
	role         string
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	finished     bool
	usage        map[string]any
}

// Add applies one SSE data payload. [DONE] and empty payloads do nothing.
// Any other payload that is not a JSON object returns an error and leaves
// the accumulator unchanged.
func (accumulator *Accumulator) Add(data []byte) error {
	payload := bytes.TrimSpace(data)
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil
	}
	var chunk map[string]any
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return fmt.Errorf("chat completion chunk is not valid JSON: %w", err)
	}
	if chunk == nil {
		return fmt.Errorf("chat completion chunk must be a JSON object")
	}
	update, err := parseChatChunk(chunk)
	if err != nil {
		return err
	}
	accumulator.apply(update)
	return nil
}

// Finished reports whether a non-empty finish_reason has been seen.
func (accumulator *Accumulator) Finished() bool {
	return accumulator.finished
}

// Completion returns a chat.completion object. The map is a snapshot: later
// Add calls do not change it. usage is omitted until a usage object arrives,
// and it keeps every field from that object.
func (accumulator *Accumulator) Completion() map[string]any {
	role := accumulator.role
	if role == "" {
		role = "assistant"
	}
	message := map[string]any{
		"role":    role,
		"content": accumulator.content.String(),
	}
	if accumulator.reasoning.Len() > 0 {
		message["reasoning_content"] = accumulator.reasoning.String()
	}
	var finishReason any
	if accumulator.finished {
		finishReason = accumulator.finishReason
	}
	completion := map[string]any{
		"object": "chat.completion",
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
	}
	if accumulator.id != "" {
		completion["id"] = accumulator.id
	}
	if accumulator.model != "" {
		completion["model"] = accumulator.model
	}
	if accumulator.hasCreated {
		completion["created"] = accumulator.created
	}
	if accumulator.usage != nil {
		completion["usage"] = utils.CloneJSONMap(accumulator.usage)
	}
	return completion
}

// Usage returns prompt_tokens and completion_tokens. Missing or non-numeric
// fields are 0. Detail fields stay on Completion's usage object.
func (accumulator *Accumulator) Usage() (prompt int, completion int) {
	return tokenCount(accumulator.usage, "prompt_tokens"), tokenCount(accumulator.usage, "completion_tokens")
}

type chatChunkUpdate struct {
	id              string
	model           string
	created         any
	hasCreated      bool
	role            string
	content         string
	reasoning       string
	finishReason    string
	setFinishReason bool
	usage           map[string]any
}

func (accumulator *Accumulator) apply(update chatChunkUpdate) {
	if update.id != "" {
		accumulator.id = update.id
	}
	if update.model != "" {
		accumulator.model = update.model
	}
	if update.hasCreated {
		accumulator.created = update.created
		accumulator.hasCreated = true
	}
	if update.role != "" {
		accumulator.role = update.role
	}
	accumulator.content.WriteString(update.content)
	accumulator.reasoning.WriteString(update.reasoning)
	if update.setFinishReason {
		accumulator.finishReason = update.finishReason
		accumulator.finished = true
	}
	if update.usage != nil {
		accumulator.usage = update.usage
	}
}

func parseChatChunk(chunk map[string]any) (chatChunkUpdate, error) {
	var update chatChunkUpdate
	identifier, err := optionalChunkString(chunk, "id")
	if err != nil {
		return chatChunkUpdate{}, err
	}
	model, err := optionalChunkString(chunk, "model")
	if err != nil {
		return chatChunkUpdate{}, err
	}
	created, hasCreated, err := optionalChunkNumber(chunk, "created")
	if err != nil {
		return chatChunkUpdate{}, err
	}
	usage, err := optionalChunkUsage(chunk)
	if err != nil {
		return chatChunkUpdate{}, err
	}
	role, content, reasoning, finishReason, setFinishReason, err := parseFirstChoice(chunk)
	if err != nil {
		return chatChunkUpdate{}, err
	}
	update.id = identifier
	update.model = model
	update.created = created
	update.hasCreated = hasCreated
	update.role = role
	update.content = content
	update.reasoning = reasoning
	update.finishReason = finishReason
	update.setFinishReason = setFinishReason
	update.usage = usage
	return update, nil
}

func parseFirstChoice(chunk map[string]any) (role, content, reasoning, finishReason string, setFinishReason bool, err error) {
	rawChoices, exists := chunk["choices"]
	if !exists || rawChoices == nil {
		return "", "", "", "", false, nil
	}
	choices, ok := rawChoices.([]any)
	if !ok {
		return "", "", "", "", false, fmt.Errorf("chat completion choices must be an array")
	}
	if len(choices) == 0 {
		return "", "", "", "", false, nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok || choice == nil {
		return "", "", "", "", false, fmt.Errorf("chat completion choice must be an object")
	}
	if rawDelta, exists := choice["delta"]; exists && rawDelta != nil {
		delta, ok := rawDelta.(map[string]any)
		if !ok {
			return "", "", "", "", false, fmt.Errorf("chat completion delta must be an object")
		}
		role, err = optionalChunkString(delta, "role")
		if err != nil {
			return "", "", "", "", false, err
		}
		content, err = optionalChunkString(delta, "content")
		if err != nil {
			return "", "", "", "", false, err
		}
		reasoning, err = optionalChunkString(delta, "reasoning_content")
		if err != nil {
			return "", "", "", "", false, err
		}
	}
	rawFinish, exists := choice["finish_reason"]
	if !exists || rawFinish == nil {
		return role, content, reasoning, "", false, nil
	}
	finishReason, ok = rawFinish.(string)
	if !ok {
		return "", "", "", "", false, fmt.Errorf("chat completion finish_reason must be a string")
	}
	if finishReason == "" {
		return role, content, reasoning, "", false, nil
	}
	return role, content, reasoning, finishReason, true, nil
}

func optionalChunkString(object map[string]any, key string) (string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("chat completion %s must be a string", key)
	}
	return text, nil
}

func optionalChunkNumber(object map[string]any, key string) (any, bool, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	switch value.(type) {
	case float64, json.Number:
		return value, true, nil
	default:
		return nil, false, fmt.Errorf("chat completion %s must be a number", key)
	}
}

func optionalChunkUsage(chunk map[string]any) (map[string]any, error) {
	value, exists := chunk["usage"]
	if !exists || value == nil {
		return nil, nil
	}
	usage, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("chat completion usage must be an object")
	}
	return utils.CloneJSONMap(usage), nil
}

func tokenCount(usage map[string]any, key string) int {
	if usage == nil {
		return 0
	}
	value, exists := usage[key]
	if !exists || value == nil {
		return 0
	}
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case int64:
		return int(number)
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}
