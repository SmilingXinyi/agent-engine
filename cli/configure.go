package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/SmilingXinyi/agent-engine/workspace"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	"github.com/knz/bubbline"
)

func configure(ctx context.Context, directory string, input io.ReadCloser, output io.Writer, signals <-chan os.Signal) error {
	prompts := newChatInput(input, output)
	defer prompts.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Configuration has one input consumer. Forward process signals through its
	// context, including while the provider is preparing the proposal.
	go func() {
		select {
		case <-runCtx.Done():
		case <-signals:
			cancel()
		}
	}()
	applied, err := workspace.Configure(runCtx, directory, protocol.ConfigureOptions{
		Review: func(ctx context.Context, changes []protocol.ConfigChange, missing []string) (bool, error) {
			for _, item := range missing {
				fmt.Fprintf(output, "Needs input · %s\n", terminalText(item, 500, false))
			}
			for _, change := range changes {
				printConfigChange(output, change)
			}
			if len(changes) == 0 {
				return false, nil
			}
			answer, err := prompts.Read(ctx, "Apply these configuration changes [y/N]? ", false)
			if errors.Is(err, io.EOF) || errors.Is(err, bubbline.ErrInterrupted) {
				return false, nil
			}
			return affirmative(answer), err
		},
	})
	if err != nil {
		return err
	}
	if applied {
		_, err = fmt.Fprintln(output, "Configuration updated. Run agent-engine chat to load it.")
	} else {
		_, err = fmt.Fprintln(output, "No configuration changes applied.")
	}
	return err
}

func printConfigChange(output io.Writer, change protocol.ConfigChange) {
	fmt.Fprintf(output, "\n.agents/%s\n", change.Name)
	if change.Before != nil {
		printConfigLines(output, "-", *change.Before)
	}
	printConfigLines(output, "+", change.After)
}
