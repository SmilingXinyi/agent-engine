package interaction

import (
	"context"
	"errors"
	"testing"

	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func TestAskUserTool(t *testing.T) {
	calls := 0
	tool, err := NewAskUserTool(func(_ context.Context, question protocol.UserQuestion) (string, error) {
		calls++
		if question.Question != "Use YAML?" {
			t.Fatalf("question = %q", question.Question)
		}
		return question.Choices[0], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{}`, `{"question": " "}`, `{"question": "Choose", "choices": ["a"]}`,
		`{"question": "Choose", "choices": ["a", "a"]}`, `{"question": "Choose", "extra": true}`,
	} {
		if _, err := tool.Execute(t.Context(), []byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if calls != 0 {
		t.Fatalf("callback received %d invalid calls", calls)
	}
	input := []byte(`{"question": "Use YAML?", "choices": ["yes", "no"]}`)
	response, err := tool.Execute(t.Context(), input)
	if err != nil || response.Text() != "yes" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tool.Execute(ctx, input); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled call reached callback: calls=%d err=%v", calls, err)
	}
}
