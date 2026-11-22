package compression_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/compression"
	"github.com/SmilingXinyi/agent-engine/compression/protocol"
)

func toolMessages(id, output string) []aiprot.Message {
	call := &aiprot.ToolCall{ID: id, Name: "shell", Arguments: json.RawMessage(`{"command":"test"}`)}
	result := aiprot.TextToolResult(output)
	result.ToolCallID, result.ToolName, result.IsError = id, "shell", true
	return []aiprot.Message{
		{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: call}}},
		{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}},
	}
}

func summaryResponse(keep int) aiprot.ModelResponse {
	answer := fmt.Sprintf(`{"user_goal":"Finish the migration","decisions":"Keep the ID","key_facts":"exit code 17","open_items":"Run tests","agreements":"Preserve the schema","keep_recent_rounds":%d}`, keep)
	return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, answer), Complete: true, Usage: aiprot.Usage{TotalTokens: 10}}
}

func TestToolPruningPreservesOriginalAndBatches(t *testing.T) {
	full := `{"stdout":"` + strings.Repeat("很长的日志", 200) + `","exit_code":17,"stderr":"permission denied","path":"src/main.go"}`
	messages := toolMessages("call-1", full)
	config := protocol.Config{ToolResultTokens: 48, ContextWindow: 200, CountTokens: func(visible []aiprot.Message) int {
		return compression.Measure(visible, 0).Tokens
	}}
	compressor, err := compression.New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := compressor.Compact(context.Background(), protocol.Request{Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Messages[1].ToolResults()[0]
	if report.TrimmedToolResults != 1 || report.SummarizedRounds != 0 || result.ToolCallID != "call-1" || !result.IsError ||
		!reflect.DeepEqual(report.Messages[0], messages[0]) || !utf8.ValidString(result.Text()) {
		t.Fatalf("unexpected tool compaction: %+v", report)
	}
	for _, fact := range []string{"exit_code: 17", "permission denied", "src/main.go"} {
		if !strings.Contains(result.Text(), fact) {
			t.Errorf("missing %q in %q", fact, result.Text())
		}
	}
	if len(report.ToolArchives) != 1 {
		t.Fatalf("archives=%+v", report.ToolArchives)
	}
	archive := report.ToolArchives[0]
	if archive.MessageIndex != 1 || archive.PartIndex != 0 || archive.Result.Text() != full ||
		report.Before.Percent <= 100 || report.After.Tokens >= report.Before.Tokens ||
		report.After.Percent != float64(report.After.Tokens)/200*100 {
		t.Fatal("archive, token reduction, or reported percentage is incorrect")
	}
	payload, err := json.Marshal(aiprot.ModelRequest{Messages: report.Messages})
	if err != nil || strings.Contains(string(payload), "original_content") || strings.Contains(string(payload), strings.Repeat("很长的日志", 50)) {
		t.Fatal("model request contains the local archive")
	}
	archive.Result.Content[0].Text = "changed clone"
	again, err := compressor.Compact(context.Background(), protocol.Request{Messages: report.Messages})
	if err != nil || again.TrimmedToolResults != 0 || len(again.ToolArchives) != 0 ||
		messages[1].ToolResults()[0].Text() != full || again.Messages[1].ToolResults()[0].Text() != result.Text() {
		t.Fatal("tool pruning mutated the input or repeated compaction changed the trimmed content")
	}
}

func TestSummaryKeepsRelevantSuffixAndActivePrompt(t *testing.T) {
	messages := []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleSystem, "system instruction"),
		aiprot.TextMessage(aiprot.RoleUser, "old user request"),
		aiprot.TextMessage(aiprot.RoleAssistant, "old decision"),
		aiprot.TextMessage(aiprot.RoleDeveloper, "developer instruction"),
		aiprot.TextMessage(aiprot.RoleUser, "active prompt"),
	}
	for _, id := range []string{"first", "second", "third"} {
		messages = append(messages, toolMessages(id, "output-"+id)...)
	}
	minKeep := 1
	compressor, err := compression.New(protocol.Config{KeepRecentRounds: &minKeep, MaxRecentRounds: 3, SummaryModel: "summary-model"}, func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		prompt := request.Messages[1].Text()
		if request.Model != "summary-model" || len(request.Tools) != 0 || request.Metadata[protocol.MetadataPurpose] != protocol.PurposeSummary {
			t.Fatal("invalid summary model request")
		}
		for _, expected := range []string{"old decision", "active prompt", "output-first", "output-second", "Retain 1 to 3"} {
			if !strings.Contains(prompt, expected) {
				t.Errorf("summary prompt missing %q", expected)
			}
		}
		return summaryResponse(2), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := compressor.Compact(context.Background(), protocol.Request{Model: "current-model", Messages: messages, MaxHistoryRounds: 3, ProtectActiveTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.SummarizedRounds != 2 || report.SummaryUsage.TotalTokens != 10 || len(report.Messages) != 8 {
		t.Fatalf("unexpected summary report: %+v", report)
	}
	if !strings.HasPrefix(report.Messages[1].Text(), protocol.SummaryMarker) || !reflect.DeepEqual(report.Messages[2:4], messages[3:5]) ||
		!reflect.DeepEqual(report.Messages[4:], messages[len(messages)-4:]) {
		t.Fatalf("summary lost protected messages or split a tool batch: %+v", report.Messages)
	}
	for _, fact := range []string{"Finish the migration", "Keep the ID", "exit code 17", "Run tests", "Preserve the schema"} {
		if !strings.Contains(report.Messages[1].Text(), fact) {
			t.Errorf("summary missing %q", fact)
		}
	}
}

func TestTokenTriggerCanReplaceAllHistoryAndChunkSummaryInput(t *testing.T) {
	messages := []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleSystem, "keep system"),
		aiprot.TextMessage(aiprot.RoleUser, "begin "+strings.Repeat("字", 2000)+" end"),
		aiprot.TextMessage(aiprot.RoleAssistant, "completed"),
	}
	keep := 0
	calls := 0
	seen := strings.Builder{}
	compressor, err := compression.New(protocol.Config{
		ContextWindow: 1024, TriggerPercent: 70, KeepRecentRounds: &keep,
	}, func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		calls++
		prompt := request.Messages[1].Text()
		_, chunk, found := strings.Cut(prompt, "Older transcript to summarize:\n")
		if !found || !utf8.ValidString(chunk) || compression.Measure(request.Messages, 0).Tokens+*request.MaxOutputTokens > 1024 {
			t.Fatal("summary chunk was malformed or exceeded the configured window")
		}
		seen.WriteString(chunk)
		if calls > 1 && !strings.Contains(prompt, "Prior summary of earlier transcript chunks:") {
			t.Fatal("subsequent chunk lacked the running summary")
		}
		return summaryResponse(0), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := compressor.Compact(context.Background(), protocol.Request{Model: "test", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || report.SummarizedRounds != 1 || report.Before.Percent <= 100 || report.After.Percent >= 70 ||
		report.SummaryUsage.TotalTokens != calls*10 || len(report.Messages) != 2 || !strings.Contains(seen.String(), messages[1].Text()) {
		t.Fatalf("history was not compacted from all chunks: %+v", report)
	}
}

func TestSummaryFailureRetainsHistory(t *testing.T) {
	messages := []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleUser, "old prompt"),
		aiprot.TextMessage(aiprot.RoleAssistant, "old reply"),
		aiprot.TextMessage(aiprot.RoleUser, "current prompt"),
	}
	keep := 1
	failure := errors.New("summary unavailable")
	for _, test := range []struct {
		name     string
		response aiprot.ModelResponse
		err      error
	}{
		{name: "invalid response", response: aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, `{"user_goal":"missing other fields"}`)}},
		{name: "provider error", response: summaryResponse(1), err: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			compressor, err := compression.New(protocol.Config{KeepRecentRounds: &keep}, func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				return test.response, test.err
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := compressor.Compact(context.Background(), protocol.Request{Model: "test", Messages: messages, MaxHistoryRounds: 1})
			if err == nil || (test.err != nil && !errors.Is(err, failure)) || report.SummarizedRounds != 0 || !reflect.DeepEqual(report.Messages, messages) {
				t.Fatalf("summary failure changed history: %+v, %v", report, err)
			}
		})
	}
}

func TestForcedSummaryFoldsInAnEarlierSummary(t *testing.T) {
	keep := 0
	calls := 0
	compressor, err := compression.New(protocol.Config{KeepRecentRounds: &keep}, func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		calls++
		prompt := request.Messages[1].Text()
		if calls == 2 && (!strings.Contains(prompt, "Finish the migration") || !strings.Contains(prompt, "new decision")) {
			t.Fatal("the next summary lacked the old summary or the new messages")
		}
		return summaryResponse(0), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	request := protocol.Request{
		Model: "test", ForceSummary: true, Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "old goal")},
	}
	first, err := compressor.Compact(ctx, request)
	if err != nil || len(first.Messages) != 1 {
		t.Fatalf("first compaction: %+v %v", first, err)
	}
	request.Messages = append(first.Messages, aiprot.TextMessage(aiprot.RoleUser, "new decision"))
	second, err := compressor.Compact(ctx, request)
	if err != nil || calls != 2 || len(second.Messages) != 1 || !strings.HasPrefix(second.Messages[0].Text(), protocol.SummaryMarker) {
		t.Fatalf("expected one replacement summary: %+v %v", second, err)
	}
}
