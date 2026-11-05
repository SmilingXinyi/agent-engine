package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

func main() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, signals); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (writer *lockedWriter) Write(content []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(content)
}

type inputLine struct {
	text string
	err  error
}

// One reader owns terminal input for the whole chat, including confirmations.
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

func run(ctx context.Context, args []string, input io.ReadCloser, output io.Writer, signals <-chan os.Signal) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agent-engine init [directory] | chat [--approval writes|all|auto] [directory]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	approval := flags.String("approval", "writes", "tool approval: writes, all or auto (chat only)")
	if args[0] != "init" && args[0] != "chat" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("expected at most one workspace directory; put flags before the directory")
	}
	directory := "."
	if flags.NArg() == 1 {
		directory = flags.Arg(0)
	}
	if args[0] == "init" {
		if err := workspace.Init(directory); err != nil {
			return err
		}
		_, err := fmt.Fprintln(output, "Workspace initialized. Existing configuration was preserved. Add .agents/state/ to your version-control ignore rules.\nExport AGENT_LLM_MODEL and AGENT_LLM_BASE_URL, then run agent-engine chat [directory].")
		return err
	}
	mode := protocol.ApprovalMode(*approval)
	if err := mode.Validate(); err != nil {
		return err
	}
	writer := &lockedWriter{writer: output}
	options := protocol.OpenOptions{
		Approval: mode,
		Warn:     func(message string) { fmt.Fprintln(writer, "[workspace]", message) },
		Hooks: loopprot.Hooks{
			OnStepEnd: func(_ context.Context, info loopprot.StepEndInfo) error {
				if text := info.Result.Response.Message.Text(); text != "" {
					_, err := fmt.Fprintln(writer, text)
					return err
				}
				return nil
			},
			OnToolStart: func(_ context.Context, info loopprot.ToolStartInfo) error {
				_, err := fmt.Fprintf(writer, "[tool] %s\n", info.Call.Name)
				return err
			},
			OnToolEnd: func(_ context.Context, info loopprot.ToolEndInfo) error {
				_, err := fmt.Fprintf(writer, "[tool result] %s error=%t\n", info.Call.Name, info.Result.IsError)
				return err
			},
		},
	}
	return chat(ctx, directory, options, input, writer, signals)
}

func terminalConfirmation(lines <-chan inputLine, output io.Writer) func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
	return func(ctx context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
		if err := ctx.Err(); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		if _, err := fmt.Fprintf(output, "Execute %s %s? [y/N]: ", info.Call.Name, info.Call.Arguments); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		select {
		case <-ctx.Done():
			return loopprot.ToolConfirmationDecision{}, ctx.Err()
		case line, ok := <-lines:
			if err := ctx.Err(); err != nil {
				return loopprot.ToolConfirmationDecision{}, err
			}
			if !ok {
				return loopprot.ToolConfirmationDecision{}, io.EOF
			}
			if line.err != nil {
				return loopprot.ToolConfirmationDecision{}, line.err
			}
			answer := strings.TrimSpace(line.text)
			approved := strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
			return loopprot.ToolConfirmationDecision{Approved: approved, Reason: "terminal approval decision"}, nil
		}
	}
}

func chat(ctx context.Context, directory string, options protocol.OpenOptions, input io.ReadCloser, output io.Writer, signals <-chan os.Signal) (err error) {
	done := make(chan struct{})
	defer close(done)
	defer input.Close()
	lines := readLines(input, done)
	options.ConfirmTool = terminalConfirmation(lines, output)
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
	for {
		fmt.Fprint(output, "> ")
		var line inputLine
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-signals:
			return nil
		case line, ok = <-lines:
		}
		if !ok || errors.Is(line.err, io.EOF) {
			return nil
		}
		if line.err != nil {
			return line.err
		}
		prompt := strings.TrimSpace(line.text)
		switch prompt {
		case "":
			continue
		case "/exit":
			return nil
		case "/help":
			fmt.Fprintln(output, "/help /history /reset /exit")
			continue
		case "/history":
			content, marshalErr := json.MarshalIndent(instance.History(), "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			fmt.Fprintln(output, string(content))
			continue
		case "/reset":
			fmt.Fprint(output, "Reset current context (audit history is retained)? [y/N]: ")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-signals:
				return nil
			case reply, ok := <-lines:
				if !ok || errors.Is(reply.err, io.EOF) {
					return nil
				}
				if reply.err != nil {
					return reply.err
				}
				if strings.EqualFold(strings.TrimSpace(reply.text), "y") || strings.EqualFold(strings.TrimSpace(reply.text), "yes") {
					if err := instance.Reset(ctx); err != nil {
						return err
					}
					fmt.Fprintln(output, "Context reset.")
				}
			}
			continue
		}
		if strings.HasPrefix(prompt, "/") {
			fmt.Fprintln(output, "Unknown command. /help for commands.")
			continue
		}
		runCtx, cancel := context.WithCancel(lifetime)
		type completed struct {
			result loopprot.Result
			err    error
		}
		completion := make(chan completed, 1)
		go func() { result, runErr := instance.Execute(runCtx, line.text); completion <- completed{result, runErr} }()
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
