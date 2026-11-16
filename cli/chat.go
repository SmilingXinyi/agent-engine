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

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	"github.com/knz/bubbline"
)

func chat(ctx context.Context, directory string, options protocol.OpenOptions, input io.ReadCloser, output io.Writer, signals <-chan os.Signal) (err error) {
	prompts := newChatInput(input, output)
	defer prompts.Close()
	options.ConfirmTool = terminalConfirmation(prompts, output)
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
			fmt.Fprintln(output, "/help /history /reset /exit")
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
		fmt.Fprintf(output, "[run] stop=%s\n", result.result.StopReason)
		if result.err != nil {
			fmt.Fprintln(output, "[error]", result.err)
		}
		if exiting {
			return ctx.Err()
		}
	}
}
