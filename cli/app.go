// Package cli provides commands and inline terminal interaction for workspace.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"

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

const usage = `Usage:
  agent-engine init [directory]
  agent-engine configure [directory]
  agent-engine chat [-c] [--approval writes|all|auto] [--verbose] [directory]
  agent-engine -c [directory]
  agent-engine help [command]

init creates YAML templates. configure proposes project-specific configuration.
Use -c to show saved conversation history before chatting.`

// Run dispatches CLI commands. Interactive commands own and close Options.Input.
func Run(ctx context.Context, args []string, options cliprot.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	input, output, signals := options.Input, options.Output, options.Signals
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "-h", "--help", "help":
		if args[0] == "help" && len(args) == 2 {
			return Run(ctx, []string{args[1], "-h"}, options)
		}
		if len(args) != 1 {
			return errors.New(usage)
		}
		_, err := fmt.Fprintln(output, usage)
		return err
	case "-c":
		args = append([]string{"chat"}, args...)
	}
	command := args[0]
	if command != "init" && command != "chat" && command != "configure" {
		return fmt.Errorf("unknown command %q; use agent-engine help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		if command == "chat" {
			fmt.Fprintln(output, "Usage: agent-engine chat [options] [directory]")
		} else {
			fmt.Fprintf(output, "Usage: agent-engine %s [directory]\n", command)
		}
		switch command {
		case "init":
			fmt.Fprintln(output, "Create workspace YAML templates and an empty prompt.")
		case "configure":
			fmt.Fprintln(output, "Generate, preview, and approve project-specific workspace configuration.")
		case "chat":
			flags.PrintDefaults()
		}
	}
	var approval string
	var resume, verbose bool
	if command == "chat" {
		flags.StringVar(&approval, "approval", "writes", "tool approval: writes, all or auto")
		flags.BoolVar(&resume, "c", false, "show saved conversation before accepting a new prompt")
		flags.BoolVar(&verbose, "verbose", false, "show tool details and model usage")
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
	switch command {
	case "init":
		if err := workspace.Init(directory); err != nil {
			return err
		}
		_, err := fmt.Fprintln(output, "Workspace initialized. Add .agents/state/ to your version-control ignore rules.\nExport AGENT_LLM_MODEL and AGENT_LLM_BASE_URL, then run agent-engine configure or chat.")
		return err
	case "configure":
		return configure(ctx, directory, input, output, signals)
	}
	mode := protocol.ApprovalMode(approval)
	if err := mode.Validate(); err != nil {
		return err
	}
	writer := &lockedWriter{writer: output}
	return chat(ctx, directory, protocol.OpenOptions{
		Approval: mode,
		Warn:     func(message string) { fmt.Fprintln(writer, "[workspace]", message) },
	}, input, writer, signals, chatDisplayOptions{resume: resume, verbose: verbose})
}
