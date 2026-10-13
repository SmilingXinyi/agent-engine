// Package agent organizes prompts and history for an independent tool-loop run.
package agent

import (
	"context"
	"errors"
	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	loop "github.com/SmilingXinyi/agent-engine/ai-tool-loop"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

type Agent struct{ loop *loop.Loop }

func New(client *ai.Client) *Agent { return &Agent{loop: loop.New(client)} }

// Execute assembles system → history → user, then delegates all execution.
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
	return agent.loop.Run(ctx, request.LoopRequest())
}
