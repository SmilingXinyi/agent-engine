// Package cli provides commands and inline terminal interaction for workspace.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	cliprot "github.com/SmilingXinyi/agent-engine/cli/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (writer *lockedWriter) Write(content []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(content)
}

// Run dispatches commands and owns the workspace opened by chat. It does not
// register process signals or exit the process. Chat closes Options.Input on
// return; init and argument validation do not take ownership of it.
func Run(ctx context.Context, args []string, options cliprot.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	input, output, signals := options.Input, options.Output, options.Signals
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
	workspaceOptions := protocol.OpenOptions{
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
	return chat(ctx, directory, workspaceOptions, input, writer, signals)
}
