package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	toolsprot "github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/compression"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	"github.com/knz/bubbline"
)

type chatDisplayOptions struct{ resume, verbose bool }

type runDisplay struct {
	output                                         io.Writer
	verbose                                        bool
	modelTime, toolTime, userWait, waitAtToolStart time.Duration
	contextTokens                                  int
	usage                                          aiprot.Usage
	finishReason                                   aiprot.FinishReason
	modelDiagnostic                                string
}

func (display *runDisplay) reset() {
	*display = runDisplay{output: display.output, verbose: display.verbose}
}

func (display *runDisplay) attach(options *protocol.OpenOptions) {
	onToolStart := options.Hooks.OnToolStart
	options.Hooks.OnToolStart = func(ctx context.Context, info loopprot.ToolStartInfo) error {
		display.waitAtToolStart = display.userWait
		if onToolStart != nil {
			return onToolStart(ctx, info)
		}
		return nil
	}
	onToolEnd := options.Hooks.OnToolEnd
	options.Hooks.OnToolEnd = func(ctx context.Context, info loopprot.ToolEndInfo) error {
		if onToolEnd != nil {
			if err := onToolEnd(ctx, info); err != nil {
				return err
			}
		}
		executionTime := max(0, info.Duration-(display.userWait-display.waitAtToolStart))
		display.toolTime += executionTime
		return display.tool(info, executionTime)
	}
	onResponse := options.OnModelResponse
	options.OnModelResponse = func(ctx context.Context, info protocol.ModelResponseInfo) error {
		if onResponse != nil {
			if err := onResponse(ctx, info); err != nil {
				return err
			}
		}
		display.modelTime += info.Duration
		display.usage.Add(info.Response.Usage)
		display.modelDiagnostic = modelFailure(info)
		if info.Request.Metadata[compressionprot.MetadataPurpose] == compressionprot.PurposeSummary {
			return nil
		}
		display.contextTokens = compression.MeasureRequest(info.Request)
		display.finishReason = info.Response.FinishReason
		if info.Error != nil {
			return nil
		}
		if text := info.Response.Message.Text(); text != "" {
			_, err := fmt.Fprintln(display.output, terminalText(text, 0, true))
			return err
		}
		return nil
	}
}

func (display *runDisplay) tool(info loopprot.ToolEndInfo, elapsed time.Duration) error {
	status := resultStatus(info.Result)
	switch {
	case errors.Is(info.Error, context.DeadlineExceeded):
		status = "timed out"
	case errors.Is(info.Error, context.Canceled) || errors.Is(info.Error, bubbline.ErrInterrupted):
		status = "cancelled"
	case info.Confirmation != nil && !info.Confirmation.Approved:
		status = "denied"
	}
	detail := ""
	if display.verbose {
		detail = " · id=" + terminalText(info.Call.ID, 80, false)
		if info.Call.Name == toolsprot.Shell {
			var result toolsprot.CommandResult
			if text := info.Result.Text(); strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &result) == nil {
				detail += fmt.Sprintf(" exit=%d", result.ExitCode)
			}
		}
		if result := terminalText(info.Result.Text(), 250, false); result != "" {
			detail += " · " + result
		}
	} else if status == "timed out" && info.Error != nil {
		detail = " · " + terminalText(info.Error.Error(), 160, false)
	}
	_, err := fmt.Fprintf(display.output, "[tool] %s · %s · %s%s\n", toolLabel(info.Call), status, duration(elapsed), detail)
	return err
}

func (display *runDisplay) finish(result loopprot.Result, err error, elapsed time.Duration) {
	label := "execution failed"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		label = "round timed out"
	case errors.Is(err, context.Canceled) || errors.Is(err, bubbline.ErrInterrupted):
		label = "cancelled"
	case err != nil:
	case result.StopReason == loopprot.StopMaxSteps:
		label = "step limit reached"
	case display.finishReason == aiprot.FinishLength:
		label = "model response truncated"
	case display.finishReason == aiprot.FinishContentFilter:
		label = "model response filtered"
	case display.finishReason == aiprot.FinishError:
		label = "model reported an error"
	case result.StopReason == loopprot.StopNatural:
		label = "model reply ended"
	}
	fmt.Fprintf(display.output, "[run] %s · model %s · tools %s · waiting %s · total %s\n",
		label, duration(display.modelTime), duration(display.toolTime), duration(display.userWait), duration(elapsed))
	if display.contextTokens > 0 {
		fmt.Fprintf(display.output, "[tokens] context estimate %d", display.contextTokens)
		usage := display.usage
		if usage != (aiprot.Usage{}) {
			fmt.Fprintf(display.output, " · provider input %d · output %d · reasoning %d · total %d", usage.InputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.TotalTokens)
		}
		fmt.Fprintln(display.output)
	}
	if err != nil {
		if display.modelDiagnostic != "" {
			fmt.Fprintln(display.output, "[error]", display.modelDiagnostic)
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, bubbline.ErrInterrupted) {
			fmt.Fprintln(display.output, "[error]", label)
		} else {
			fmt.Fprintln(display.output, "[error]", terminalText(err.Error(), 500, false))
		}
	}
}

var upstreamID = regexp.MustCompile(`(?i)request[_ -]id[:=][[:space:]]*([a-zA-Z0-9_.-]{1,160})`)

func modelFailure(info protocol.ModelResponseInfo) string {
	var failure *aiprot.Error
	if !errors.As(info.Error, &failure) {
		return ""
	}
	summary := fmt.Sprintf("model=%s api=%s code=%s attempts=%d", terminalText(info.Request.Model, 120, false), info.APIType, failure.Code, failure.CallInfo.Attempts)
	if failure.CallInfo.StatusCode != 0 {
		summary += fmt.Sprintf(" HTTP %d", failure.CallInfo.StatusCode)
	}
	headers := http.Header(failure.CallInfo.Headers)
	requestID := headers.Get("X-Request-Id")
	if requestID == "" {
		requestID = headers.Get("Request-Id")
	}
	if requestID == "" {
		var body struct {
			RequestID string `json:"request_id"`
			Error     struct {
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		_ = json.Unmarshal(failure.Body, &body)
		requestID = body.RequestID
		if requestID == "" {
			requestID = body.Error.RequestID
		}
	}
	if requestID == "" {
		if match := upstreamID.FindStringSubmatch(failure.Message); len(match) == 2 {
			requestID = match[1]
		}
	}
	if requestID != "" {
		summary += " request_id=" + terminalText(requestID, 160, false)
	}
	return summary
}

func toolLabel(call aiprot.ToolCall) string {
	name := terminalText(call.Name, 80, false)
	if call.Name == toolsprot.Shell {
		purpose, command := shellCallDetails(call)
		if purpose != "" {
			return name + " · " + purpose
		}
		if fields := strings.Fields(command); len(fields) > 0 && regexpCommand.MatchString(fields[0]) {
			return name + " - " + fields[0]
		}
		return name
	}
	var arguments struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return name
	}
	var action string
	switch call.Name {
	case toolsprot.ReadFile, toolsprot.WriteFile, toolsprot.ListDirectory, toolsprot.ApplyPatch:
		action = arguments.Path
	case "get_skill":
		action = arguments.Name
	}
	if action == "" {
		return name
	}
	return name + " - " + terminalText(action, 120, false)
}

func shellCallDetails(call aiprot.ToolCall) (purpose, command string) {
	if call.Name != toolsprot.Shell {
		return "", ""
	}
	var arguments struct {
		Purpose string `json:"purpose"`
		Command string `json:"command"`
	}
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return "", ""
	}
	return strings.TrimSpace(terminalText(arguments.Purpose, 120, false)), arguments.Command
}

var regexpCommand = regexp.MustCompile(`^[a-zA-Z0-9._/-]{1,80}$`)

func resultStatus(result aiprot.ToolResult) string {
	if result.IsError {
		return "failed"
	}
	return "succeeded"
}

func duration(elapsed time.Duration) string { return elapsed.Round(time.Millisecond).String() }

func terminalText(value string, limit int, multiline bool) string {
	var buffer strings.Builder
	count := 0
	for _, character := range value {
		if limit > 0 && count == limit {
			buffer.WriteRune('…')
			break
		}
		switch {
		case multiline && (character == '\n' || character == '\t'):
			buffer.WriteRune(character)
		case unicode.IsControl(character) || unicode.Is(unicode.Cf, character):
			if multiline {
				quoted := strconv.QuoteRuneToASCII(character)
				buffer.WriteString(quoted[1 : len(quoted)-1])
			} else {
				buffer.WriteByte(' ')
			}
		default:
			buffer.WriteRune(character)
		}
		count++
	}
	return buffer.String()
}

func printConfigLines(output io.Writer, marker, content string) {
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		fmt.Fprintf(output, "%s %s\n", marker, terminalText(line, 0, true))
	}
}
