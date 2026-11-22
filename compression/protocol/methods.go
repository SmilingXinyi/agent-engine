package protocol

import (
	"fmt"
	"math"
	"strings"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/utils"
)

func (config Config) Validate() error {
	if config.ContextWindow < 0 || config.ToolResultTokens < 0 || config.MaxRecentRounds < 0 ||
		(config.KeepRecentRounds != nil && *config.KeepRecentRounds < 0) {
		return fmt.Errorf("compression limits must be nonnegative")
	}
	if math.IsNaN(config.TriggerPercent) || config.TriggerPercent < 0 || config.TriggerPercent > 100 {
		return fmt.Errorf("compression triggerPercent must be between 0 and 100")
	}
	defaults := config.WithDefaults()
	if defaults.MaxRecentRounds < *defaults.KeepRecentRounds {
		return fmt.Errorf("compression maxRecentRounds must be at least keepRecentRounds")
	}
	return nil
}

func (config Config) WithDefaults() Config {
	if config.TriggerPercent == 0 {
		config.TriggerPercent = 80
	}
	if config.ToolResultTokens == 0 {
		config.ToolResultTokens = 1024
	}
	if config.KeepRecentRounds == nil {
		keep := 2
		config.KeepRecentRounds = &keep
	}
	if config.MaxRecentRounds == 0 && *config.KeepRecentRounds > 0 {
		config.MaxRecentRounds = max(4, *config.KeepRecentRounds)
	}
	return config.Clone()
}

func (config Config) Clone() Config {
	if config.KeepRecentRounds != nil {
		keep := *config.KeepRecentRounds
		config.KeepRecentRounds = &keep
	}
	config.SummaryProviderOptions = utils.CloneJSONMap(config.SummaryProviderOptions)
	return config
}

func NewContextUsage(tokens, window int) ContextUsage {
	usage := ContextUsage{Tokens: tokens, ContextWindow: window}
	if window > 0 {
		usage.Percent = float64(tokens) / float64(window) * 100
	}
	return usage
}

// NewSummaryMessage marks a synthetic assistant message for future compaction.
func NewSummaryMessage(text string) aiprot.Message {
	message := aiprot.TextMessage(aiprot.RoleAssistant, SummaryMarker+"\n"+text)
	message.Metadata = map[string]any{summaryMetadataKey: true}
	return message
}

// IsSummary reports whether a message carries the compression marker.
func IsSummary(message aiprot.Message) bool {
	marked, _ := message.Metadata[summaryMetadataKey].(bool)
	return marked && message.Role == aiprot.RoleAssistant && strings.HasPrefix(message.Text(), SummaryMarker+"\n")
}
