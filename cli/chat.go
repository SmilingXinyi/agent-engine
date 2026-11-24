package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	"github.com/knz/bubbline"
)

func chat(ctx context.Context, directory string, options protocol.OpenOptions, input io.ReadCloser, output io.Writer, signals <-chan os.Signal, settings ...chatDisplayOptions) (err error) {
	prompts := newChatInput(input, output)
	defer prompts.Close()
	var settingsValue chatDisplayOptions
	if len(settings) > 0 {
		settingsValue = settings[0]
	}
	display := &runDisplay{output: output, verbose: settingsValue.verbose}
	display.attach(&options)
	confirmation := terminalConfirmation(prompts, output)
	options.ConfirmTool = func(ctx context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
		started := time.Now()
		defer func() { display.userWait += time.Since(started) }()
		return confirmation(ctx, info)
	}
	options.AskUser = terminalQuestion(prompts, output, display)
	options.RequireShellPurpose = true
	lifetime, cancelLifetime := context.WithCancel(ctx)
	defer cancelLifetime()
	type opened struct {
		workspace *workspace.Workspace
		err       error
	}
	opening := make(chan opened, 1)
	go func() {
		instance, openErr := workspace.Open(lifetime, directory, options)
		opening <- opened{instance, openErr}
	}()
	var instance *workspace.Workspace
	select {
	case result := <-opening:
		instance, err = result.workspace, result.err
	case <-signals:
		cancelLifetime()
		result := <-opening
		if result.workspace != nil {
			_ = result.workspace.Close()
		}
		return nil
	case <-ctx.Done():
		cancelLifetime()
		result := <-opening
		if result.workspace != nil {
			_ = result.workspace.Close()
		}
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, instance.Close()) }()
	fmt.Fprintln(output, "Workspace ready. /help for commands. History: .agents/state/history.ndjson")
	if settingsValue.resume {
		replayHistory(output, instance.History())
	}
	if prompts.editor != nil {
		fmt.Fprintln(output, "Enter: send | Ctrl+J / Alt+Enter: newline | Up/Down / Ctrl+R: history")
	}
	for {
		prompts.SetHistory(instance.History())
		text, readErr := readIdle(ctx, prompts, signals, "> ", true)
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, bubbline.ErrInterrupted) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		prompt := strings.TrimSpace(text)
		switch prompt {
		case "":
			continue
		case "/exit":
			return nil
		case "/help":
			fmt.Fprintln(output, "/help show commands · /history show JSON · /reset clear context · /exit close workspace")
			if prompts.editor != nil {
				fmt.Fprintln(output, "Enter: send; Ctrl+J / Ctrl+O / Alt+Enter: newline; Up/Down: edit/history; Ctrl+R: history search; Ctrl+C: clear draft, exit when empty; Ctrl+D: EOF on an empty line.")
			}
			continue
		case "/history":
			content, marshalErr := json.MarshalIndent(instance.History(), "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			fmt.Fprintln(output, string(content))
			continue
		case "/reset":
			fmt.Fprintln(output, "Reset current context (audit history is retained)?")
			reply, readErr := readIdle(ctx, prompts, signals, "Reset [y/N]: ", false)
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if errors.Is(readErr, bubbline.ErrInterrupted) {
				continue
			}
			if readErr != nil {
				return readErr
			}
			if strings.EqualFold(strings.TrimSpace(reply), "y") || strings.EqualFold(strings.TrimSpace(reply), "yes") {
				if err := instance.Reset(ctx); err != nil {
					return err
				}
				fmt.Fprintln(output, "Context reset.")
			}
			continue
		}
		if strings.HasPrefix(prompt, "/") && !strings.ContainsAny(prompt, "\r\n") {
			fmt.Fprintln(output, "Unknown command. /help for commands.")
			continue
		}
		runCtx, cancel := context.WithCancel(lifetime)
		display.reset()
		started := time.Now()
		type completed struct {
			result loopprot.Result
			err    error
		}
		completion := make(chan completed, 1)
		go func() { result, runErr := instance.Execute(runCtx, text); completion <- completed{result, runErr} }()
		interrupted, exiting := false, false
		var result completed
	awaitRun:
		for {
			select {
			case result = <-completion:
				break awaitRun
			case signal := <-signals:
				if interrupted || signal == syscall.SIGTERM {
					exiting = true
				}
				interrupted = true
				cancel()
				fmt.Fprintln(output, "\nCancelling current execution...")
			case <-ctx.Done():
				cancel()
				result = <-completion
				exiting = true
				break awaitRun
			}
		}
		cancel()
		display.finish(result.result, result.err, time.Since(started))
		if exiting {
			return ctx.Err()
		}
	}
}

func replayHistory(output io.Writer, messages []aiprot.Message) {
	if len(messages) == 0 {
		fmt.Fprintln(output, "No saved messages.")
		return
	}
	fmt.Fprintln(output, "Saved conversation")
	for _, message := range messages {
		if text := message.Text(); text != "" && message.Role != aiprot.RoleTool {
			fmt.Fprintf(output, "[%s] %s\n", message.Role, terminalText(text, 0, true))
		}
		for _, call := range message.ToolCalls() {
			fmt.Fprintf(output, "[tool] %s\n", toolLabel(call))
		}
		for _, result := range message.ToolResults() {
			fmt.Fprintf(output, "[tool] %s · %s\n", terminalText(result.ToolName, 80, false), resultStatus(result))
		}
	}
}
