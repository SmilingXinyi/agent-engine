package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/knz/bubbline"
)

type inputLine struct {
	text string
	err  error
}

// chatInput owns stdin. Only the active prompt reads from the terminal; a
// plain input stream keeps its single cancellable channel consumer.
type chatInput struct {
	reader         io.ReadCloser
	output         io.Writer
	lines          <-chan inputLine
	done           chan struct{}
	closeOnce      sync.Once
	editor         *bubbline.Editor
	terminalOutput *os.File
}

func newChatInput(reader io.ReadCloser, output io.Writer) *chatInput {
	input := &chatInput{reader: reader, output: output, done: make(chan struct{})}
	stdin, isFile := reader.(*os.File)
	stdout := outputFile(output)
	if isFile && stdout != nil && term.IsTerminal(stdin.Fd()) && term.IsTerminal(stdout.Fd()) && os.Getenv("TERM") != "dumb" {
		input.editor = newEditor(true)
		input.terminalOutput = stdout
	} else {
		input.lines = readLines(reader, input.done)
	}
	return input
}

func outputFile(output io.Writer) *os.File {
	switch writer := output.(type) {
	case *os.File:
		return writer
	case *lockedWriter:
		return outputFile(writer.writer)
	default:
		return nil
	}
}

// Keep Bubble Tea's TTY size detection while serializing renderer output with
// the CLI's progress/error messages. Bubble Tea does not own this file.
type terminalWriter struct {
	*os.File
	writer io.Writer
}

func (writer terminalWriter) Write(content []byte) (int, error) { return writer.writer.Write(content) }

func newEditor(multiline bool) *bubbline.Editor {
	editor := bubbline.New()
	editor.Prompt = "> "
	editor.NextPrompt = "· "
	editor.MaxHeight = 12
	editor.KeyMap.AlwaysComplete.Unbind()
	editor.KeyMap.AlwaysNewline.Unbind()
	if multiline {
		editor.KeyMap.AlwaysNewline = key.NewBinding(
			key.WithKeys("ctrl+j", "ctrl+o", "alt+enter", "alt+\r"),
			key.WithHelp("C-j/M-enter", "newline"),
		)
	}
	// Do not enable editor-launched processes or suspend the entire agent
	// process group. Run cancellation remains owned by the CLI.
	editor.KeyMap.SignalQuit.Unbind()
	editor.KeyMap.SignalTTYStop.Unbind()
	editor.Reset()
	return editor
}

func (input *chatInput) SetHistory(messages []aiprot.Message) {
	if input.editor == nil {
		return
	}
	var prompts []string
	for _, message := range messages {
		if message.Role == aiprot.RoleUser && strings.TrimSpace(message.Text()) != "" {
			prompts = append(prompts, message.Text())
		}
	}
	input.editor.SetHistory(prompts)
}

func (input *chatInput) Read(ctx context.Context, prompt string, multiline bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if input.editor == nil {
		if _, err := fmt.Fprint(input.output, prompt); err != nil {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case line, ok := <-input.lines:
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if !ok {
				return "", io.EOF
			}
			return line.text, line.err
		}
	}
	editor := input.editor
	if !multiline {
		// Approval input has no history and must not inherit a draft or a
		// previously typed "yes" from the conversation editor.
		editor = newEditor(false)
	}
	editor.Prompt = prompt
	editor.Reset()
	model := &promptModel{Editor: editor, confirmation: !multiline}
	program := tea.NewProgram(model,
		tea.WithInput(input.reader),
		tea.WithOutput(terminalWriter{File: input.terminalOutput, writer: input.output}),
		tea.WithoutSignalHandler(),
	)
	// Context cancellation must quit gracefully: Bubble Tea's forced shutdown
	// closes its cancel reader without joining the input loop on Linux.
	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			program.Quit()
		case <-finished:
		}
	}()
	_, err := program.Run()
	close(finished)
	<-watcherDone
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return editor.Value(), editor.Err
}

// Confirmation Ctrl+C always interrupts, even after typing a partial answer.
// Conversation Ctrl+C uses Bubbline's clear-draft / interrupt-empty behavior.
type promptModel struct {
	*bubbline.Editor
	confirmation bool
}

func (model *promptModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if keyMessage, ok := message.(tea.KeyMsg); ok && model.confirmation && keyMessage.Type == tea.KeyCtrlC {
		model.Err = bubbline.ErrInterrupted
		return model, tea.Quit
	}
	_, command := model.Editor.Update(message)
	return model, command
}

func (input *chatInput) Close() {
	input.closeOnce.Do(func() {
		close(input.done)
		_ = input.reader.Close()
	})
}

// readIdle cancels and joins the active editor before returning on an external
// signal, so the next phase never competes for stdin or leaves raw mode active.
func readIdle(ctx context.Context, input *chatInput, signals <-chan os.Signal, prompt string, multiline bool) (string, error) {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan inputLine, 1)
	go func() {
		text, err := input.Read(readCtx, prompt, multiline)
		result <- inputLine{text: text, err: err}
	}()
	select {
	case line := <-result:
		return line.text, line.err
	case <-ctx.Done():
		cancel()
		<-result
		return "", ctx.Err()
	case <-signals:
		cancel()
		<-result
		return "", io.EOF
	}
}

func terminalConfirmation(input *chatInput, output io.Writer) func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
	return func(ctx context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
		if err := ctx.Err(); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		if _, err := fmt.Fprintf(output, "Execute %s %s?\n", info.Call.Name, info.Call.Arguments); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		text, err := input.Read(ctx, "Approve [y/N]: ", false)
		if err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		if err := ctx.Err(); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		answer := strings.TrimSpace(text)
		approved := strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
		return loopprot.ToolConfirmationDecision{Approved: approved, Reason: "terminal approval decision"}, nil
	}
}

// One reader owns plain input, including confirmations. No plain reader is
// started for a TTY because Bubbline must be its only reader.
func readLines(input io.Reader, done <-chan struct{}) <-chan inputLine {
	lines := make(chan inputLine)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(input)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				select {
				case lines <- inputLine{text: strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")}:
				case <-done:
					return
				}
			}
			if err != nil {
				select {
				case lines <- inputLine{err: err}:
				case <-done:
				}
				return
			}
		}
	}()
	return lines
}
