package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

type cliProvider func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)

func (cliProvider) Name() string { return "cli-test" }
func (provider cliProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider(ctx, request)
}

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents/workspace.json"), []byte("{\"model\":\"test\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"write_file\":{}}}"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInitAndArgumentErrors(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	for range 2 {
		if err := run(t.Context(), []string{"init", root}, io.NopCloser(strings.NewReader("")), &output, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(output.String(), ".agents/state/") {
		t.Fatal(output.String())
	}
	for _, args := range [][]string{nil, {"invalid"}, {"chat", "--approval", "invalid", root}, {"chat", root, "extra"}} {
		if err := run(t.Context(), args, io.NopCloser(strings.NewReader("")), &output, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestChatConfirmationHistoryAndReset(t *testing.T) {
	root := project(t)
	requests := 0
	options := protocol.OpenOptions{Provider: cliProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		requests++
		if requests == 1 {
			call := aiprot.ToolCall{ID: "write", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"note\",\"content\":\"hello\"}")}
			return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	})}
	var output bytes.Buffer
	input := io.NopCloser(strings.NewReader("/help\nwrite a note\ny\n/history\n/reset\nn\n/history\n/reset\ny\n/history\n/exit\n"))
	if err := chat(t.Context(), root, options, input, &lockedWriter{writer: &output}, nil); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note"))
	if err != nil || string(content) != "hello" {
		t.Fatalf("content=%s err=%v", content, err)
	}
	if !strings.Contains(output.String(), "Execute write_file") || !strings.Contains(output.String(), "Context reset.") {
		t.Fatal(output.String())
	}
	reopened, err := workspace.Open(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.History()) != 0 {
		t.Fatal("reset did not persist")
	}
	log, err := os.ReadFile(filepath.Join(root, ".agents/state/history.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(log, []byte("\"type\":\"context_reset\"")) != 1 {
		t.Fatal("reset denial was ignored")
	}
}

func TestConfirmationEOFAndCancelledInputAreNotApproved(t *testing.T) {
	for _, eof := range []bool{true, false} {
		reader, writer := io.Pipe()
		prompts := newChatInput(reader, io.Discard)
		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan error, 1)
		go func() {
			decision, err := terminalConfirmation(prompts, io.Discard)(ctx, loopprot.ToolConfirmationInfo{})
			if decision.Approved {
				finished <- errors.New("unexpected approval")
				return
			}
			finished <- err
		}()
		if eof {
			writer.Close()
		} else {
			cancel()
		}
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("expected input failure")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("confirmation stuck")
		}
		if !eof {
			go func() { _, _ = io.WriteString(writer, "next prompt\n") }()
			select {
			case line := <-prompts.lines:
				if line.text != "next prompt" {
					t.Fatal(line)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled confirmation consumed future input")
			}
		}
		cancel()
		prompts.Close()
		writer.Close()
	}
}

type observingWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	updates chan string
}

func (writer *observingWriter) Write(content []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	count, err := writer.buffer.Write(content)
	select {
	case writer.updates <- string(content):
	default:
	}
	return count, err
}
func waitOutput(t *testing.T, writer *observingWriter, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case update := <-writer.updates:
			if strings.Contains(update, text) {
				return
			}
		case <-timer.C:
			t.Fatalf("missing output %q", text)
		}
	}
}

func TestInterruptCancelsRunThenAcceptsNextPrompt(t *testing.T) {
	root := project(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	signals := make(chan os.Signal, 2)
	output := &observingWriter{updates: make(chan string, 50)}
	started := make(chan struct{})
	requests := 0
	options := protocol.OpenOptions{Provider: cliProvider(func(ctx context.Context, _ aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		requests++
		if requests == 1 {
			close(started)
			<-ctx.Done()
			return aiprot.ModelResponse{}, ctx.Err()
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "continued")}, nil
	})}
	finished := make(chan error, 1)
	go func() { finished <- chat(t.Context(), root, options, reader, output, signals) }()
	if _, err := io.WriteString(writer, "first\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model not started")
	}
	signals <- os.Interrupt
	waitOutput(t, output, "[run] stop=error")
	if _, err := io.WriteString(writer, "second\n"); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, output, "[run] stop=natural")
	if _, err := io.WriteString(writer, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat did not exit")
	}
	if requests != 2 {
		t.Fatal(requests)
	}
}

func TestEOFOrIdleInterruptClosesWorkspace(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		root := project(t)
		reader, writer := io.Pipe()
		output := &observingWriter{updates: make(chan string, 20)}
		signals := make(chan os.Signal, 1)
		options := protocol.OpenOptions{Provider: cliProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			t.Error("idle chat called model")
			return aiprot.ModelResponse{}, nil
		})}
		finished := make(chan error, 1)
		go func() { finished <- chat(t.Context(), root, options, reader, output, signals) }()
		waitOutput(t, output, "Workspace ready")
		if interrupt {
			signals <- os.Interrupt
		} else {
			writer.Close()
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("chat did not exit")
		}
		writer.Close()
		instance, err := workspace.Open(t.Context(), root, options)
		if err != nil {
			t.Fatal("workspace lock leaked", err)
		}
		if err := instance.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
