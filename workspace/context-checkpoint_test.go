package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

const checkpointSummary = `{"user_goal":"long-lived goal","decisions":"","key_facts":"","open_items":"","agreements":"","keep_recent_rounds":1}`

func newCheckpointProject(t *testing.T) string {
	t.Helper()
	root := newProject(t)
	keep := 1
	writeJSON(t, filepath.Join(root, ".agents/workspace.yaml"), protocol.Config{
		Model: "test-model", MaxSteps: 1, MaxHistoryRounds: 2,
		Compression: &compressionprot.Config{KeepRecentRounds: &keep, MaxRecentRounds: 1},
	})
	return root
}

func TestContextCheckpointSkipsOldRawHistoryAcrossRunsAndReopen(t *testing.T) {
	root := newCheckpointProject(t)
	const oldRaw = "original above the boundary"
	const followup = "followup kept in the journal"
	const interrupted = "interrupted prompt below the boundary"
	const bounded = "prompt that hits the step limit"
	const newSystem = "updated workspace instructions"
	summaryCalls := 0
	generatingAfterCheckpoint := false
	reopened := false
	options := protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		var text strings.Builder
		for _, message := range request.Messages {
			text.WriteString(message.Text())
			text.WriteByte('\n')
		}
		if generatingAfterCheckpoint && strings.Contains(text.String(), oldRaw) {
			t.Fatal("request sent original text above the checkpoint")
		}
		if request.Metadata[compressionprot.MetadataPurpose] == compressionprot.PurposeSummary {
			summaryCalls++
			if summaryCalls == 1 && !strings.Contains(text.String(), oldRaw) {
				t.Fatal("first summary omitted the older journal text")
			}
			if reopened {
				for _, expected := range []string{"long-lived goal", followup, bounded, interrupted, "was not executed", "outcome is unknown"} {
					if !strings.Contains(text.String(), expected) {
						t.Errorf("incremental summary omitted %q", expected)
					}
				}
			}
			return answer(checkpointSummary), nil
		}
		if generatingAfterCheckpoint && !strings.Contains(text.String(), "long-lived goal") {
			t.Error("model request omitted previous summary")
		}
		if reopened && (!strings.Contains(request.Messages[0].Text(), newSystem) ||
			strings.Count(text.String(), "You are a local workspace assistant.") != 1) {
			t.Error("model request did not use exactly one current workspace system prompt")
		}
		if request.Messages[len(request.Messages)-1].Text() == bounded {
			return calls(aiprot.ToolCall{ID: "bounded", Name: "test", Arguments: json.RawMessage("{}")}), nil
		}
		return answer("recorded"), nil
	})}
	instance := openTest(t, root, options)
	for _, prompt := range []string{oldRaw, "second turn", "create checkpoint"} {
		if _, err := instance.Execute(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	if summaryCalls != 1 {
		t.Fatalf("summary calls=%d", summaryCalls)
	}
	generatingAfterCheckpoint = true
	if _, err := instance.Execute(t.Context(), followup); err != nil {
		t.Fatal(err)
	}
	if summaryCalls != 1 {
		t.Fatalf("old history triggered another summary: %d", summaryCalls)
	}

	sequence := instance.checkpoint.Sequence
	if result, err := instance.Execute(t.Context(), bounded); err != nil || result.StopReason != loopprot.StopMaxSteps {
		t.Fatalf("bounded run result=%v, err=%v", result.StopReason, err)
	}
	durable, err := readContextCheckpoint(instance.contextCheckpointPath())
	if err != nil || durable == nil || durable.Sequence != sequence || instance.checkpoint.Sequence != sequence {
		t.Fatalf("bounded run advanced the checkpoint: %+v, %v", durable, err)
	}
	// The bounded run may make a summary for its own request.
	summariesBeforeReopen := summaryCalls

	// Simulate an interrupted run written after the last checkpoint.
	journal := instance.history
	runID := "interrupted"
	user := aiprot.TextMessage(aiprot.RoleUser, interrupted)
	model := calls(aiprot.ToolCall{ID: "pending", Name: "test", Arguments: json.RawMessage("{}")}).Message
	for _, entry := range []struct {
		kind protocol.EventType
		data protocol.EventData
	}{
		{protocol.RunStarted, protocol.EventData{}},
		{protocol.UserMessage, protocol.EventData{Message: &user}},
		{protocol.ModelMessage, protocol.EventData{Message: &model}},
		{protocol.ToolStarted, protocol.EventData{ToolCallID: "pending"}},
	} {
		if err := journal.append(entry.kind, runID, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".agents", "prompt.md"), []byte(newSystem))
	reopened = true
	instance = openTest(t, root, options)
	if _, err := instance.Execute(t.Context(), "after reopen"); err != nil {
		t.Fatal(err)
	}
	if summaryCalls != summariesBeforeReopen+1 || instance.History()[0].Text() != oldRaw {
		t.Fatalf("summary calls=%d or original journal lost", summaryCalls)
	}
	if err := instance.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(instance.History()) != 0 {
		t.Fatal("reset left history visible")
	}
	if _, err := os.Stat(instance.contextCheckpointPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reset left the checkpoint: %v", err)
	}
}

func TestContextCheckpointRejectsCorruptOrMismatchedFile(t *testing.T) {
	for _, scenario := range []string{"corrupt", "mismatched"} {
		t.Run(scenario, func(t *testing.T) {
			root := newCheckpointProject(t)
			instance := openTest(t, root, protocol.OpenOptions{})
			if _, err := instance.Execute(t.Context(), "local history"); err != nil {
				t.Fatal(err)
			}
			if err := instance.Close(); err != nil {
				t.Fatal(err)
			}
			path := instance.contextCheckpointPath()
			if scenario == "corrupt" {
				writeFile(t, path, []byte("{invalid"))
			} else {
				writeJSON(t, path, contextCheckpoint{
					Version: contextCheckpointVersion, Sequence: 4, RunID: "wrong run",
					Messages: []aiprot.Message{compressionprot.NewSummaryMessage("summary")},
				})
			}
			modelCalls := 0
			instance = openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				modelCalls++
				if len(request.Messages) != 2 || request.Messages[1].Text() != "new history" {
					t.Fatalf("reset reused previous history: %+v", request.Messages)
				}
				return answer("done"), nil
			})})
			if _, err := instance.Execute(t.Context(), "must not send"); err == nil || !strings.Contains(err.Error(), "checkpoint") || modelCalls != 0 {
				t.Fatalf("invalid checkpoint was used: %v, calls=%d", err, modelCalls)
			}
			if len(instance.History()) != 2 || instance.History()[0].Text() != "local history" {
				t.Fatal("invalid checkpoint hid the local journal")
			}
			if err := instance.Reset(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := instance.Execute(t.Context(), "new history"); err != nil || modelCalls != 1 {
				t.Fatalf("reset did not allow a fresh run: %v, calls=%d", err, modelCalls)
			}
		})
	}
}

func TestContextCheckpointWriteFailureKeepsCompletedJournal(t *testing.T) {
	root := newCheckpointProject(t)
	instance := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if request.Metadata[compressionprot.MetadataPurpose] == compressionprot.PurposeSummary {
			return answer(checkpointSummary), nil
		}
		return answer("done"), nil
	})})
	for _, prompt := range []string{"first", "second"} {
		if _, err := instance.Execute(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	path := instance.contextCheckpointPath()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Execute(t.Context(), "third"); err == nil || !strings.Contains(err.Error(), "save context checkpoint") {
		t.Fatalf("did not surface a checkpoint write failure: %v", err)
	}
	journal := events(t, root)
	if instance.checkpoint != nil || len(instance.History()) != 6 || journal[len(journal)-1].Type != protocol.RunEnded {
		t.Fatal("a checkpoint failure changed the completed journal")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Execute(t.Context(), "retry"); err != nil || instance.checkpoint == nil {
		t.Fatalf("checkpoint could not be saved after write failure: %v", err)
	}
}
