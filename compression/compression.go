// Package compression compacts model context while retaining usable history.
package compression

import (
	"context"
	"errors"
	"fmt"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/compression/protocol"
)

type Compressor struct {
	config   protocol.Config
	generate protocol.GenerateFunc
}

func New(config protocol.Config, generate protocol.GenerateFunc) (*Compressor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Compressor{config: config.WithDefaults(), generate: generate}, nil
}

// Compact trims long tool outputs, then summarizes older rounds when a trigger
// is reached. Result messages may share read-only data with the input.
func (compressor *Compressor) Compact(ctx context.Context, request protocol.Request) (protocol.Result, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Result{}, err
	}
	if request.MaxHistoryRounds < 0 {
		return protocol.Result{}, errors.New("compression history round limit must be nonnegative")
	}
	config := compressor.config
	messages, archives := trimTools(request.Messages, config.ToolResultTokens)
	report := protocol.Result{
		Messages: messages, TrimmedToolResults: len(archives), ToolArchives: archives,
		Before: compressor.Measure(request.Messages),
	}
	report.After = report.Before
	if len(archives) > 0 {
		report.After = compressor.Measure(messages)
	}
	starts, lastUser := roundStarts(messages)
	minKeep, maxKeep := *config.KeepRecentRounds, config.MaxRecentRounds
	if request.ProtectActiveTurn {
		minKeep, maxKeep = max(1, minKeep), max(1, maxKeep)
	} else {
		lastUser = -1
	}
	overRounds := request.MaxHistoryRounds > 0 && len(starts) > request.MaxHistoryRounds
	overTokens := config.ContextWindow > 0 && report.After.Percent >= config.TriggerPercent
	if (!request.ForceSummary && !overRounds && !overTokens) || len(starts) <= minKeep {
		return report, nil
	}

	transcript := olderTranscript(messages[:recentCutoff(messages, starts, minKeep)], lastUser)
	if transcript == "" {
		return report, nil
	}
	model := config.SummaryModel
	if model == "" {
		model = request.Model
	}
	maxKeep = min(maxKeep, len(starts)-1)
	if overRounds {
		maxKeep = min(maxKeep, max(minKeep, request.MaxHistoryRounds))
	}
	summary, usage, err := compressor.summarize(ctx, model, messages, starts, lastUser, minKeep, maxKeep, transcript, request.ProviderOptions)
	report.SummaryUsage = usage
	if err != nil {
		return report, fmt.Errorf("summarize conversation: %w", err)
	}
	keep := min(max(*summary.KeepRecentRounds, minKeep), maxKeep)
	summaryMessage := summary.message()
	// Optional raw rounds should never push a token-triggered request over the
	// threshold when removing them can still bring the context down.
	for {
		count := len(starts) - keep
		compacted := replaceOlder(messages, recentCutoff(messages, starts, keep), lastUser, summaryMessage)
		usage := compressor.Measure(compacted)
		if !overTokens || usage.Percent < config.TriggerPercent || keep == minKeep {
			// A token-only compaction must make progress even with pinned input.
			if !request.ForceSummary && !overRounds && usage.Tokens >= report.After.Tokens {
				return report, nil
			}
			report.Messages, report.After, report.SummarizedRounds = compacted, usage, count
			return report, nil
		}
		keep--
	}
}

func recentCutoff(messages []aiprot.Message, starts []int, keep int) int {
	if keep == 0 {
		return len(messages)
	}
	return starts[len(starts)-keep]
}

// Measure uses the configured tokenizer or the provider-neutral estimate.
func (compressor *Compressor) Measure(messages []aiprot.Message) protocol.ContextUsage {
	if counter := compressor.config.CountTokens; counter != nil {
		return protocol.NewContextUsage(counter(messages), compressor.config.ContextWindow)
	}
	return Measure(messages, compressor.config.ContextWindow)
}

// A round consists of input followed by one assistant response and its tool
// results. An autonomous assistant reply starts another round.
func roundStarts(messages []aiprot.Message) (starts []int, lastUser int) {
	lastUser = -1
	hasAssistant := false
	for index, message := range messages {
		if isInstruction(message) || protocol.IsSummary(message) {
			continue
		}
		if message.Role == aiprot.RoleUser {
			lastUser = index
		}
		if len(starts) == 0 || (hasAssistant && message.Role != aiprot.RoleTool) {
			starts = append(starts, index)
			hasAssistant = false
		}
		if message.Role == aiprot.RoleAssistant {
			hasAssistant = true
		}
	}
	return starts, lastUser
}

func isInstruction(message aiprot.Message) bool {
	return message.Role == aiprot.RoleSystem || message.Role == aiprot.RoleDeveloper
}

func replaceOlder(messages []aiprot.Message, cutoff, lastUser int, summary aiprot.Message) []aiprot.Message {
	compacted := make([]aiprot.Message, 0, len(messages)-cutoff+2)
	inserted := false
	for index, message := range messages[:cutoff] {
		switch {
		case isInstruction(message):
			compacted = append(compacted, message)
		default:
			if !inserted {
				compacted = append(compacted, summary)
				inserted = true
			}
			if index == lastUser {
				compacted = append(compacted, message)
			}
		}
	}
	return append(compacted, messages[cutoff:]...)
}
