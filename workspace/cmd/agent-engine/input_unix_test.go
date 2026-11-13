//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/knz/bubbline"
)

type terminalTranscript struct {
	mu      sync.Mutex
	content bytes.Buffer
	changed chan struct{}
}

func (transcript *terminalTranscript) Write(content []byte) (int, error) {
	transcript.mu.Lock()
	count, err := transcript.content.Write(content)
	transcript.mu.Unlock()
	select {
	case transcript.changed <- struct{}{}:
	default:
	}
	return count, err
}

func (transcript *terminalTranscript) contains(text string) bool {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return strings.Contains(transcript.content.String(), text)
}

func (transcript *terminalTranscript) wait(t *testing.T, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if transcript.contains(text) {
			return
		}
		select {
		case <-transcript.changed:
		case <-timer.C:
			transcript.mu.Lock()
			content := transcript.content.String()
			transcript.mu.Unlock()
			t.Fatalf("terminal did not display %q; output: %q", text, content)
		}
	}
}

func terminalPair(t *testing.T) (*os.File, *os.File, *terminalTranscript) {
	t.Helper()
	t.Setenv("TERM", "xterm-256color")
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("PTY is required on supported platforms: %v", err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	transcript := &terminalTranscript{changed: make(chan struct{}, 1)}
	stopped := make(chan struct{})
	go func() { _, _ = io.Copy(transcript, master); close(stopped) }()
	t.Cleanup(func() {
		_ = slave.Close()
		_ = master.Close()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Error("PTY output reader leaked")
		}
	})
	return master, slave, transcript
}

func terminalRead(t *testing.T, input *chatInput, ctx context.Context, prompt string, multiline bool) <-chan inputLine {
	t.Helper()
	result := make(chan inputLine, 1)
	go func() { text, err := input.Read(ctx, prompt, multiline); result <- inputLine{text: text, err: err} }()
	return result
}

func readResult(t *testing.T, result <-chan inputLine) inputLine {
	t.Helper()
	select {
	case line := <-result:
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("terminal input did not finish")
		return inputLine{}
	}
}

func typeKeys(t *testing.T, master *os.File, text string) {
	t.Helper()
	if _, err := io.WriteString(master, text); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalMultilineHistoryAndStateRestore(t *testing.T) {
	master, slave, transcript := terminalPair(t)
	initial, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	input := newChatInput(slave, &lockedWriter{writer: slave})
	t.Cleanup(input.Close)
	if input.editor == nil {
		t.Fatal("real TTY did not select Bubbline")
	}
	first := terminalRead(t, input, t.Context(), "first> ", true)
	transcript.wait(t, "first> ")
	typeKeys(t, master, "第一行\x0a第二行\x1b\rthird\r")
	line := readResult(t, first)
	want := "第一行\n第二行\nthird"
	if line.err != nil || line.text != want {
		t.Fatalf("input=%q err=%v", line.text, line.err)
	}
	restored, err := term.GetState(slave.Fd())
	if err != nil || !reflect.DeepEqual(initial, restored) {
		t.Fatalf("terminal was not restored: %v", err)
	}
	input.SetHistory([]aiprot.Message{
		aiprot.TextMessage(aiprot.RoleUser, want),
		aiprot.TextMessage(aiprot.RoleAssistant, "must not appear in input history"),
	})
	history := terminalRead(t, input, t.Context(), "history> ", true)
	transcript.wait(t, "history> ")
	typeKeys(t, master, "\x1b[A\r")
	line = readResult(t, history)
	if line.err != nil || line.text != want {
		t.Fatalf("history=%q err=%v", line.text, line.err)
	}
	input.SetHistory(nil)
	empty := terminalRead(t, input, t.Context(), "reset> ", true)
	transcript.wait(t, "reset> ")
	typeKeys(t, master, "\x1b[A\r")
	line = readResult(t, empty)
	if line.err != nil || line.text != "" {
		t.Fatalf("reset recalled input: %+v", line)
	}
}

func TestTerminalCancellationDoesNotConsumeNextPrompt(t *testing.T) {
	master, slave, transcript := terminalPair(t)
	initial, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	input := newChatInput(slave, slave)
	t.Cleanup(input.Close)
	ctx, cancel := context.WithCancel(t.Context())
	first := terminalRead(t, input, ctx, "cancel> ", true)
	transcript.wait(t, "cancel> ")
	cancel()
	if line := readResult(t, first); !errors.Is(line.err, context.Canceled) {
		t.Fatal(line.err)
	}
	restored, err := term.GetState(slave.Fd())
	if err != nil || !reflect.DeepEqual(initial, restored) {
		t.Fatal("cancel left the terminal in raw mode", err)
	}
	next := terminalRead(t, input, t.Context(), "next> ", true)
	transcript.wait(t, "next> ")
	typeKeys(t, master, "next prompt\r")
	if line := readResult(t, next); line.err != nil || line.text != "next prompt" {
		t.Fatalf("next=%+v", line)
	}
}

func TestTerminalBracketedPasteWaitsForSubmission(t *testing.T) {
	master, slave, transcript := terminalPair(t)
	input := newChatInput(slave, slave)
	t.Cleanup(input.Close)
	result := terminalRead(t, input, t.Context(), "paste> ", true)
	transcript.wait(t, "paste> ")
	typeKeys(t, master, "\x1b[200~first\n/exit\nlast pasted line\x1b[201~")
	transcript.wait(t, "last pasted line")
	select {
	case line := <-result:
		t.Fatalf("paste was submitted before Enter: %+v", line)
	default:
	}
	typeKeys(t, master, "\r")
	if line := readResult(t, result); line.err != nil || line.text != "first\n/exit\nlast pasted line" {
		t.Fatalf("paste=%+v", line)
	}
}

func TestTerminalConfirmationHasNoHistoryAndInterruptsPartialAnswer(t *testing.T) {
	master, slave, transcript := terminalPair(t)
	input := newChatInput(slave, slave)
	t.Cleanup(input.Close)
	input.SetHistory([]aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "yes")})
	result := make(chan error, 1)
	confirmed := false
	go func() {
		decision, err := terminalConfirmation(input, slave)(t.Context(), loopprot.ToolConfirmationInfo{Call: aiprot.ToolCall{Name: "write_file", Arguments: []byte("{}")}})
		confirmed = decision.Approved
		result <- err
	}()
	transcript.wait(t, "Approve [y/N]: ")
	typeKeys(t, master, "\x1b[A\r")
	select {
	case err := <-result:
		if err != nil || confirmed {
			t.Fatalf("history approved a tool: %t %v", confirmed, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation stuck")
	}
	next := terminalRead(t, input, t.Context(), "partial> ", false)
	transcript.wait(t, "partial> ")
	typeKeys(t, master, "y\x03")
	if line := readResult(t, next); !errors.Is(line.err, bubbline.ErrInterrupted) {
		t.Fatalf("partial answer was not cancelled: %+v", line)
	}
}

func TestTerminalDraftInterruptAndEOF(t *testing.T) {
	master, slave, transcript := terminalPair(t)
	input := newChatInput(slave, slave)
	t.Cleanup(input.Close)
	draft := terminalRead(t, input, t.Context(), "draft> ", true)
	transcript.wait(t, "draft> ")
	typeKeys(t, master, "discard this\x03kept\r")
	if line := readResult(t, draft); line.err != nil || line.text != "kept" {
		t.Fatalf("draft=%+v", line)
	}
	exit := terminalRead(t, input, t.Context(), "exit> ", true)
	transcript.wait(t, "exit> ")
	typeKeys(t, master, "\x03")
	if line := readResult(t, exit); !errors.Is(line.err, bubbline.ErrInterrupted) {
		t.Fatal(line.err)
	}
	eof := terminalRead(t, input, t.Context(), "eof> ", true)
	transcript.wait(t, "eof> ")
	typeKeys(t, master, "\x04")
	if line := readResult(t, eof); !errors.Is(line.err, io.EOF) {
		t.Fatal(line.err)
	}
}

func TestTTYChatPreservesMultilineThroughAgentAndReopen(t *testing.T) {
	root := project(t)
	master, slave, transcript := terminalPair(t)
	initial, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var received string
	options := protocol.OpenOptions{Provider: cliProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		received = request.Messages[len(request.Messages)-1].Text()
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "received")}, nil
	})}
	finished := make(chan error, 1)
	go func() { finished <- chat(t.Context(), root, options, slave, &lockedWriter{writer: slave}, nil) }()
	transcript.wait(t, "> ")
	typeKeys(t, master, "/a/project/file\x0aplease inspect\r")
	transcript.wait(t, "[run] stop=natural")
	// Wait for the next editor to enter raw mode before sending /exit.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := term.GetState(slave.Fd())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(initial, state) {
			break
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("next prompt did not enter raw mode")
		}
	}
	typeKeys(t, master, "/exit\r")
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat did not exit")
	}
	if received != "/a/project/file\nplease inspect" {
		t.Fatalf("model received %q", received)
	}
	reopened, err := workspace.Open(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history := reopened.History()
	if len(history) != 2 || history[0].Text() != received {
		t.Fatalf("persisted multiline history=%+v", history)
	}
}
