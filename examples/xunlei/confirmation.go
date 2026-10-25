package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func executionOptions(input io.ReadCloser, output io.Writer, maxSteps int) loopprot.RunOptions {
	// The vision adapter applies the final message limit after adding images.
	// Keep the original task available until that limit is applied.
	options := loopprot.RunOptions{MaxSteps: maxSteps, MaxHistoryRounds: maxSteps, Hooks: executionHooks(output)}
	// Agent interprets the mode; only supply the terminal UI when explicitly
	// configured so an empty environment preserves unattended execution.
	if strings.TrimSpace(os.Getenv("AGENT_TOOL_CONFIRMATION")) != "" {
		options.ConfirmTool = terminalConfirmation(input, output)
	}
	return options
}

func terminalConfirmation(input io.ReadCloser, output io.Writer) func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
	reader := bufio.NewReader(input)
	return func(ctx context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
		if err := ctx.Err(); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		if _, err := fmt.Fprintf(output, "\n[tool confirmation] step=%d id=%q name=%s\narguments=%s\n是否执行？输入 y/yes 批准，其他输入拒绝 [y/N]: ", info.Step, info.Call.ID, info.Call.Name, printableJSON(info.Call.Arguments)); err != nil {
			return loopprot.ToolConfirmationDecision{}, err
		}
		type answer struct {
			line string
			err  error
		}
		answers := make(chan answer, 1)
		// The buffered channel lets the reader finish if cancellation wins.
		go func() {
			line, err := reader.ReadString('\n')
			answers <- answer{line: line, err: err}
		}()
		select {
		case <-ctx.Done():
			_ = input.Close()
			return loopprot.ToolConfirmationDecision{}, ctx.Err()
		case reply := <-answers:
			if err := ctx.Err(); err != nil {
				_ = input.Close()
				return loopprot.ToolConfirmationDecision{}, err
			}
			if reply.err != nil {
				return loopprot.ToolConfirmationDecision{}, fmt.Errorf("读取工具确认输入失败: %w", reply.err)
			}
			response := strings.TrimSpace(reply.line)
			approved := strings.EqualFold(response, "y") || strings.EqualFold(response, "yes")
			decision := loopprot.ToolConfirmationDecision{Approved: approved}
			if !approved {
				decision.Reason = "用户在终端拒绝执行"
			}
			return decision, nil
		}
	}
}
