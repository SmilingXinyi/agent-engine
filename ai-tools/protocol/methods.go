package protocol

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

// LoadConfig reads configuration only; it never builds tools or executes commands.
func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read built-in tool config %q: %w", path, err)
	}
	config, err := ParseConfig(string(content))
	if err != nil {
		return Config{}, fmt.Errorf("load built-in tool config %q: %w", path, err)
	}
	return config, nil
}

// ParseConfig requires an explicit builtinTools object and rejects unknown fields.
func ParseConfig(content string) (Config, error) {
	var config Config
	if err := utils.DecodeObject([]byte(content), &config); err != nil {
		return Config{}, fmt.Errorf("parse built-in tool config: %w", err)
	}
	if config.BuiltinTools == nil {
		return Config{}, fmt.Errorf("built-in tool config requires a builtinTools object")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// UnmarshalJSON rejects null entries and null disabled flags rather than
// silently enabling a tool through JSON's null-to-zero-value behavior.
func (setting *ToolConfig) UnmarshalJSON(content []byte) error {
	var fields struct {
		Disabled json.RawMessage `json:"disabled"`
	}
	if err := utils.DecodeObject(content, &fields); err != nil {
		return err
	}
	setting.Disabled = false
	if len(fields.Disabled) == 0 {
		return nil
	}
	var disabled *bool
	if err := json.Unmarshal(fields.Disabled, &disabled); err != nil {
		return err
	}
	if disabled == nil {
		return fmt.Errorf("disabled must be a boolean")
	}
	setting.Disabled = *disabled
	return nil
}

// Validate checks all names, including disabled entries, to catch configuration typos.
// Backend options are checked by constructors only for enabled tools.
func (config Config) Validate() error {
	for name := range config.BuiltinTools {
		switch name {
		case ReadFile, WriteFile, ApplyPatch, ListDirectory, Shell, AskUser:
		default:
			return fmt.Errorf("unknown built-in tool %q", name)
		}
	}
	return nil
}

// EnabledNames returns a deterministic build order without changing the config.
func (config Config) EnabledNames() []string {
	names := make([]string, 0, len(config.BuiltinTools))
	for name, setting := range config.BuiltinTools {
		if !setting.Disabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (question UserQuestion) Validate() error {
	if strings.TrimSpace(question.Question) == "" || utf8.RuneCountInString(question.Question) > 1000 {
		return fmt.Errorf("question must contain 1 to 1000 characters")
	}
	if question.Choices != nil && (len(question.Choices) < 2 || len(question.Choices) > 8) {
		return fmt.Errorf("provide two to eight choices or omit them")
	}
	seen := make(map[string]bool, len(question.Choices))
	for _, choice := range question.Choices {
		if strings.TrimSpace(choice) == "" || utf8.RuneCountInString(choice) > 160 || seen[choice] {
			return fmt.Errorf("choices must be distinct and contain 1 to 160 characters")
		}
		seen[choice] = true
	}
	return nil
}
