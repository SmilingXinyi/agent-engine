package openai

import (
	"reflect"
	"testing"
)

func TestAccumulatorBuildsChatCompletion(t *testing.T) {
	var accumulator Accumulator
	if err := accumulator.Add([]byte(`{"id":"chatcmpl-1","model":"m","created":123,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`)); err != nil {
		t.Fatal(err)
	}
	if accumulator.Finished() {
		t.Fatal("finish_reason has not arrived")
	}
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"content":"lo","reasoning_content":"think"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{}},{"index":1,"delta":{"content":"ignored","reasoning_content":"no"},"finish_reason":"length"}],"usage":null}`)); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"reasoning_content":"ing","tool_calls":[{"id":"call-1"}]},"finish_reason":"stop"}]}`)); err != nil {
		t.Fatal(err)
	}
	if !accumulator.Finished() {
		t.Fatal("expected finish_reason")
	}
	prompt, completionTokens := accumulator.Usage()
	if prompt != 0 || completionTokens != 0 {
		t.Fatalf("usage before the usage frame = %d %d", prompt, completionTokens)
	}
	if err := accumulator.Add([]byte(`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11,"completion_tokens_details":{"reasoning_tokens":4}}}`)); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"[DONE]", "  [DONE]\n", "", "   "} {
		if err := accumulator.Add([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}

	prompt, completionTokens = accumulator.Usage()
	if prompt != 9 || completionTokens != 2 {
		t.Fatalf("usage = %d %d", prompt, completionTokens)
	}
	got := accumulator.Completion()
	want := map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion",
		"created": float64(123),
		"model":   "m",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":              "assistant",
					"content":           "Hello",
					"reasoning_content": "thinking",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(9),
			"completion_tokens": float64(2),
			"total_tokens":      float64(11),
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": float64(4),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("completion = %#v", got)
	}

	got["usage"].(map[string]any)["prompt_tokens"] = float64(0)
	got["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] = "mutated"
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"content":"!"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if got["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] != "mutated" {
		t.Fatal("Completion snapshot changed")
	}
	prompt, _ = accumulator.Usage()
	if prompt != 9 || accumulator.Completion()["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] != "Hello!" {
		t.Fatalf("accumulator changed with the snapshot: %#v", accumulator.Completion())
	}
}

func TestAccumulatorKeepsStateWhenChunkIsInvalid(t *testing.T) {
	var accumulator Accumulator
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"content":"ok"}}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"{", "[]", "null", `"text"`, `{"choices":[{"delta":{"content":1}}]}`, `{"usage":[]}`, `{"created":"now"}`} {
		if err := accumulator.Add([]byte(payload)); err == nil {
			t.Fatalf("payload %q was accepted", payload)
		}
	}
	message := accumulator.Completion()["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "ok" || message["role"] != "assistant" || accumulator.Finished() {
		t.Fatalf("completion = %#v finished = %v", accumulator.Completion(), accumulator.Finished())
	}
	if _, exists := accumulator.Completion()["usage"]; exists {
		t.Fatal("invalid usage was stored")
	}
}

func TestAccumulatorPreservesUsageDetailsWithoutTokenCounts(t *testing.T) {
	var accumulator Accumulator
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Add([]byte(`{"usage":{"completion_tokens_details":{"reasoning_tokens":3}}}`)); err != nil {
		t.Fatal(err)
	}
	prompt, completionTokens := accumulator.Usage()
	if !accumulator.Finished() || prompt != 0 || completionTokens != 0 {
		t.Fatalf("finished=%v usage=%d %d", accumulator.Finished(), prompt, completionTokens)
	}
	usage := accumulator.Completion()["usage"].(map[string]any)
	details := usage["completion_tokens_details"].(map[string]any)
	if details["reasoning_tokens"] != float64(3) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestAccumulatorIgnoresEmptyFinishReason(t *testing.T) {
	var accumulator Accumulator
	if err := accumulator.Add([]byte(`{"choices":[{"delta":{"content":"x"},"finish_reason":""}]}`)); err != nil {
		t.Fatal(err)
	}
	if accumulator.Finished() {
		t.Fatal("empty finish_reason finished the accumulator")
	}
	choice := accumulator.Completion()["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != nil {
		t.Fatalf("finish_reason = %#v", choice["finish_reason"])
	}
}
