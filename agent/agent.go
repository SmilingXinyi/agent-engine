// Package agent organizes prompts and history for an independent tool-loop run.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	loop "github.com/SmilingXinyi/agent-engine/ai-tool-loop"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

type Agent struct{ loop *loop.Loop }

func New(client *ai.Client) *Agent { return &Agent{loop: loop.New(client)} }

// Execute assembles system → history → user, applies AGENT_TOOL_CONFIRMATION
// from the process environment, then delegates execution to the tool loop.
func (agent *Agent) Execute(ctx context.Context, request protocol.Request) (loopprot.Result, error) {
	if err := ctx.Err(); err != nil {
		return loopprot.Result{StopReason: loopprot.StopError}, err
	}
	if agent == nil || agent.loop == nil {
		return loopprot.Result{StopReason: loopprot.StopError}, errors.New("agent is required")
	}
	if err := request.Validate(); err != nil {
		return loopprot.Result{StopReason: loopprot.StopError}, err
	}
	options, err := applyToolConfirmationEnv(request.Options)
	if err != nil {
		return loopprot.Result{StopReason: loopprot.StopError}, err
	}
	request.Options = options
	return agent.loop.Run(ctx, request.LoopRequest())
}

func applyToolConfirmationEnv(options loopprot.RunOptions) (loopprot.RunOptions, error) {
	mode := strings.TrimSpace(os.Getenv("AGENT_TOOL_CONFIRMATION"))
	switch mode {
	case "":
		return options, nil
	case "off":
		options.ConfirmTool = nil
		options.ToolDenialPolicy = loopprot.ToolDenialContinue
	case "continue", "stop":
		if options.ConfirmTool == nil {
			return options, fmt.Errorf("AGENT_TOOL_CONFIRMATION=%s requires Options.ConfirmTool", mode)
		}
		options.ToolDenialPolicy = loopprot.ToolDenialPolicy(mode)
	default:
		return options, fmt.Errorf("invalid AGENT_TOOL_CONFIRMATION %q: expected off, continue or stop", mode)
	}
	return options, nil
}
