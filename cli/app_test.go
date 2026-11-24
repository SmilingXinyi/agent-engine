package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	cliprot "github.com/SmilingXinyi/agent-engine/cli/protocol"
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
	if err := os.WriteFile(filepath.Join(root, ".agents/workspace.yaml"), []byte("{\"model\":\"test\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents/tools.yaml"), []byte("{\"builtinTools\":{\"write_file\":{}}}"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInitAndArgumentErrors(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	for range 2 {
		if err := Run(t.Context(), []string{"init", root}, cliprot.Options{Input: io.NopCloser(strings.NewReader("")), Output: &output}); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(output.String(), ".agents/state/") {
		t.Fatal(output.String())
	}
	for _, args := range [][]string{nil, {"invalid"}, {"chat", "--approval", "invalid", root}, {"chat", root, "extra"}} {
		if err := Run(t.Context(), args, cliprot.Options{Input: io.NopCloser(strings.NewReader("")), Output: &output}); err == nil {
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
	waitOutput(t, output, "[run] cancelled")
	if _, err := io.WriteString(writer, "second\n"); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, output, "[run] model reply ended")
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

// Exercise the exported runner, including output hooks and workspace ownership.
func TestRunChatLifecycle(t *testing.T) {
	root := project(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"role":"assistant","content":"hello from model"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	t.Setenv("AGENT_LLM_BASE_URL", server.URL+"/v1")
	t.Setenv("AGENT_LLM_TOKEN", "")
	t.Setenv("AGENT_LLM_API_KEY", "")
	var output bytes.Buffer
	input := &ownedInput{Reader: strings.NewReader("hello\n/exit\n")}
	if err := Run(t.Context(), []string{"chat", root}, cliprot.Options{Input: input, Output: &output}); err != nil {
		t.Fatal(err)
	}
	if !input.closed || !strings.Contains(output.String(), "hello from model") || !strings.Contains(output.String(), "[run] model reply ended") {
		t.Fatalf("closed=%t output=%s", input.closed, output.String())
	}
	instance, err := workspace.Open(t.Context(), root, protocol.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if len(instance.History()) != 2 {
		t.Fatalf("history=%v", instance.History())
	}
}

type ownedInput struct {
	*strings.Reader
	closed bool
}

func (input *ownedInput) Close() error { input.closed = true; return nil }

func TestRunValidatesStreams(t *testing.T) {
	for _, options := range []cliprot.Options{{}, {Input: io.NopCloser(strings.NewReader(""))}, {Output: io.Discard}} {
		if err := Run(t.Context(), []string{"init", t.TempDir()}, options); err == nil {
			t.Fatal("accepted missing stream")
		}
	}
}

func TestContinueReplaysHistoryWithoutModelRequest(t *testing.T) {
	root := project(t)
	session, err := workspace.Open(t.Context(), root, protocol.OpenOptions{
		Provider: cliProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "saved reply")}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(t.Context(), "saved prompt"); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_LLM_BASE_URL", "http://127.0.0.1:1/v1")
	for _, args := range [][]string{{"-c", root}, {"chat", "-c", root}} {
		var output bytes.Buffer
		if err := Run(t.Context(), args, cliprot.Options{Input: io.NopCloser(strings.NewReader("/exit\n")), Output: &output}); err != nil {
			t.Fatal(err)
		}
		transcript := output.String()
		if !strings.Contains(transcript, "[user] saved prompt\n[assistant] saved reply") || strings.Contains(transcript, "[run]") {
			t.Fatal(transcript)
		}
	}
}

func TestModelRetryConfigurationAndDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name                       string
		status, retries, wantCalls int
	}{
		{"bad request", http.StatusBadRequest, 2, 1},
		{"temporary failure", http.StatusServiceUnavailable, 2, 2},
		{"disabled retries", http.StatusServiceUnavailable, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := project(t)
			config := fmt.Sprintf("model: test\nmodelMaxRetries: %d\nreasoningEffort: low\nproviderOptions:\n  thinking: {type: disabled}\n", test.retries)
			if err := os.WriteFile(filepath.Join(root, ".agents/workspace.yaml"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					ReasoningEffort string            `json:"reasoning_effort"`
					Thinking        map[string]string `json:"thinking"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ReasoningEffort != "low" || payload.Thinking["type"] != "disabled" {
					t.Errorf("model payload=%+v err=%v", payload, err)
				}
				w.Header().Set("Content-Type", "application/json")
				if attempts.Add(1) == 1 || test.status == http.StatusBadRequest {
					w.WriteHeader(test.status)
					io.WriteString(w, `{"error":{"message":"private provider details (request_id: sample-upstream-id)"}}`)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"retry worked"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
			}))
			defer server.Close()
			t.Setenv("AGENT_LLM_BASE_URL", server.URL)
			var output bytes.Buffer
			if err := Run(t.Context(), []string{"chat", root}, cliprot.Options{Input: io.NopCloser(strings.NewReader("test prompt\n/exit\n")), Output: &output}); err != nil {
				t.Fatal(err)
			}
			if int(attempts.Load()) != test.wantCalls {
				t.Fatalf("attempts=%d output=%s", attempts.Load(), output.String())
			}
			transcript := output.String()
			if test.wantCalls == 2 {
				if !strings.Contains(transcript, "retry worked") || !strings.Contains(transcript, "provider input 10") {
					t.Fatal(transcript)
				}
			} else {
				if !strings.Contains(transcript, "model=test api=chat_completions") ||
					!strings.Contains(transcript, fmt.Sprintf("attempts=1 HTTP %d request_id=sample-upstream-id", test.status)) ||
					strings.Contains(transcript, "private provider details") {
					t.Fatal(transcript)
				}
			}
		})
	}
}

func TestRunConfigurePreviewsAndApplies(t *testing.T) {
	root := project(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		proposal := `{"files":{"prompt.md":"Follow the project instructions.\n","mcp.yaml":"mcpServers:\n  example: {disabled: true}\n"},"missing":["Configure the MCP address before enabling it."]}`
		response := map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": proposal}, "finish_reason": "stop"}}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("AGENT_LLM_BASE_URL", server.URL)
	for _, test := range []struct{ answer, content string }{{"n\n", ""}, {"y\n", "Follow the project instructions.\n"}} {
		var output bytes.Buffer
		if err := Run(t.Context(), []string{"configure", root}, cliprot.Options{Input: io.NopCloser(strings.NewReader(test.answer)), Output: &output}); err != nil {
			t.Fatal(err)
		}
		transcript := output.String()
		if !strings.Contains(transcript, "Needs input · Configure the MCP address") ||
			!strings.Contains(transcript, "+ Follow the project instructions.") {
			t.Fatal(transcript)
		}
		content, err := os.ReadFile(filepath.Join(root, ".agents/prompt.md"))
		if err != nil || string(content) != test.content {
			t.Fatalf("prompt=%q err=%v", content, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, ".agents/mcp.yaml"))
	if err != nil || !strings.Contains(string(content), "example: {disabled: true}") || requests.Load() != 2 {
		t.Fatalf("MCP=%q requests=%d err=%v", content, requests.Load(), err)
	}
}
