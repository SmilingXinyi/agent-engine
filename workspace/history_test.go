package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

func seedInterrupted(t *testing.T, root string, started bool) {
	t.Helper()
	log, err := openHistory(filepath.Join(root, ".agents/state/history.ndjson"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer log.file.Close()
	appendEvent := func(kind protocol.EventType, data protocol.EventData) {
		t.Helper()
		if err := log.append(kind, "interrupted", data); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(protocol.RunStarted, protocol.EventData{})
	user := aiprot.TextMessage(aiprot.RoleUser, "unfinished")
	user.Metadata = map[string]any{"large": json.Number("900719925474099312345")}
	appendEvent(protocol.UserMessage, protocol.EventData{Message: &user})
	response := calls(
		aiprot.ToolCall{ID: "first", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"first\",\"content\":\"x\",\"number\":900719925474099312345}")},
		aiprot.ToolCall{ID: "second", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"second\",\"content\":\"x\"}")},
	)
	appendEvent(protocol.ModelMessage, protocol.EventData{Message: &response.Message})
	if started {
		appendEvent(protocol.ToolStarted, protocol.EventData{ToolCallID: "first"})
	}
}

func TestInterruptedReplayDistinguishesUnknownAndNotExecuted(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-started", true: "unknown"}[started], func(t *testing.T) {
			root := newProject(t)
			seedInterrupted(t, root, started)
			if started {
				writeFile(t, filepath.Join(root, "first"), []byte("external effect"))
			}
			var warnings []string
			count := 0
			options := protocol.OpenOptions{Warn: func(message string) { warnings = append(warnings, message) }, Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				count++
				return answer("done"), nil
			})}
			instance := openTest(t, root, options)
			if count != 0 || len(warnings) != 3 {
				t.Fatalf("count=%d warnings=%v", count, warnings)
			}
			history := instance.History()
			if len(history) != 4 {
				t.Fatal(history)
			}
			if err := (agentprot.Request{Model: "test", History: history}).Validate(); err != nil {
				t.Fatal(err)
			}
			if history[0].Metadata["large"] != json.Number("900719925474099312345") {
				t.Fatal("number precision lost")
			}
			if !bytes.Contains(history[1].ToolCalls()[0].Arguments, []byte("900719925474099312345")) {
				t.Fatal("argument precision lost")
			}
			first := history[2].ToolResults()[0]
			if !first.IsError || strings.Contains(first.Text(), "unknown") != started {
				t.Fatalf("first result=%+v", first)
			}
			if !strings.Contains(history[3].ToolResults()[0].Text(), "not executed") {
				t.Fatal("wrong unstarted recovery")
			}
			before := len(events(t, root))
			if err := instance.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openTest(t, root, options)
			if len(events(t, root)) != before {
				t.Fatal("recovery was appended twice")
			}
			if _, err := os.Stat(filepath.Join(root, "second")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("pending tool replayed")
			}
			if _, err := reopened.Execute(t.Context(), "continue"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTruncatedTailAndCorruptRecords(t *testing.T) {
	root := newProject(t)
	instance := openTest(t, root, protocol.OpenOptions{})
	if _, err := instance.Execute(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".agents/state/history.ndjson")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, append(append([]byte(nil), original...), []byte("{\"version\":1")...))
	warnings := 0
	repaired := openTest(t, root, protocol.OpenOptions{Warn: func(string) { warnings++ }})
	if warnings != 1 {
		t.Fatal(warnings)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("tail repair changed committed records")
	}
	if err := repaired.Close(); err != nil {
		t.Fatal(err)
	}
	var first protocol.Event
	if err := protocol.DecodeJSON(bytes.Split(original, []byte("\n"))[0], &first); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"syntax", "version", "sequence", "event", "data", "blank"} {
		t.Run(kind, func(t *testing.T) {
			event := first
			var content []byte
			switch kind {
			case "syntax":
				content = []byte("{broken}\n")
			case "blank":
				content = []byte("\n")
			default:
				switch kind {
				case "version":
					event.Version = 99
				case "sequence":
					event.Sequence = 7
				case "event":
					event.Type = "future_event"
				case "data":
					event.Data.ToolCallID = "unexpected"
				}
				content, _ = json.Marshal(event)
				content = append(content, '\n')
			}
			content = append(content, original...)
			writeFile(t, path, content)
			if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
				t.Fatal("corrupt history accepted")
			}
			actual, _ := os.ReadFile(path)
			if !bytes.Equal(content, actual) {
				t.Fatal("corrupt history was modified")
			}
		})
	}
	writeFile(t, path, original)
	_ = openTest(t, root, protocol.OpenOptions{}) // failed replay released the lock
}

func TestStateSymlinksAreRejected(t *testing.T) {
	for _, name := range []string{"workspace.lock", "history.ndjson"} {
		t.Run(name, func(t *testing.T) {
			root := newProject(t)
			target := filepath.Join(t.TempDir(), "outside")
			writeFile(t, target, []byte("do not modify"))
			if err := os.Symlink(target, filepath.Join(root, ".agents/state", name)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
				t.Fatal("state symlink accepted")
			}
			actual, _ := os.ReadFile(target)
			if string(actual) != "do not modify" {
				t.Fatal("outside file modified")
			}
		})
	}
}

func TestCrashAfterCompletedToolHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_CRASH_HELPER") != "1" {
		return
	}
	instance, err := Open(context.Background(), os.Getenv("WORKSPACE_CRASH_ROOT"), protocol.OpenOptions{
		Approval: protocol.ApprovalAuto,
		Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			return calls(
				aiprot.ToolCall{ID: "first", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"first\",\"content\":\"completed\"}")},
				aiprot.ToolCall{ID: "second", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"second\",\"content\":\"must not execute\"}")},
			), nil
		}),
		Hooks: loopprot.Hooks{OnToolEnd: func(context.Context, loopprot.ToolEndInfo) error { os.Exit(0); return nil }},
	})
	if err != nil {
		os.Exit(2)
	}
	_, _ = instance.Execute(context.Background(), "write")
	os.Exit(3)
}

func TestProcessCrashPreservesCompletedResult(t *testing.T) {
	root := newProject(t)
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"write_file\":{}}}"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestCrashAfterCompletedToolHelper$")
	command.Env = append(os.Environ(), "WORKSPACE_CRASH_HELPER=1", "WORKSPACE_CRASH_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %s %v", output, err)
	}
	instance := openTest(t, root, protocol.OpenOptions{})
	history := instance.History()
	if len(history) != 4 || history[2].ToolResults()[0].IsError || !history[3].ToolResults()[0].IsError {
		t.Fatalf("history=%+v", history)
	}
	if _, err := os.Stat(filepath.Join(root, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending call replayed")
	}
	if content, err := os.ReadFile(filepath.Join(root, "first")); err != nil || string(content) != "completed" {
		t.Fatalf("completed effect=%s err=%v", content, err)
	}
}
