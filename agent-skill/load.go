// Package agentskill loads local skills and exposes their instructions as a
// tool. It does not select skills, run scripts, or own an Agent.
package agentskill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SmilingXinyi/agent-engine/agent-skill/protocol"
	"gopkg.in/yaml.v3"
)

// Load discovers SKILL.md files recursively, including the root itself. Once
// a skill is found its subdirectories are resources, not additional skills.
// A missing root is an empty optional catalog; malformed skills are errors.
func Load(root string) ([]protocol.Skill, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("skills directory is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills path is not a directory: %s", root)
	}
	var skills []protocol.Skill
	seen := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		skillPath := filepath.Join(path, "SKILL.md")
		content, err := os.ReadFile(skillPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read skill %q: %w", skillPath, err)
		}
		skill, err := Parse(string(content))
		if err != nil {
			return fmt.Errorf("parse skill %q: %w", skillPath, err)
		}
		if previous, exists := seen[skill.Name]; exists {
			return fmt.Errorf("duplicate skill %q in %q and %q", skill.Name, previous, skillPath)
		}
		skill.Path = skillPath
		seen[skill.Name] = skillPath
		skills = append(skills, skill)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(skills, func(first, second int) bool { return skills[first].Name < skills[second].Name })
	return skills, nil
}

// Parse reads YAML frontmatter and preserves the Markdown body verbatim.
// Only name and description are consumed; additional metadata is allowed.
func Parse(content string) (protocol.Skill, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	opening, rest, found := strings.Cut(content, "\n")
	if !found || strings.TrimSuffix(opening, "\r") != "---" {
		return protocol.Skill{}, fmt.Errorf("missing opening frontmatter delimiter")
	}
	for offset := 0; offset < len(rest); {
		line, body, hasNewline := strings.Cut(rest[offset:], "\n")
		if strings.TrimSuffix(line, "\r") == "---" {
			var metadata struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal([]byte(rest[:offset]), &metadata); err != nil {
				return protocol.Skill{}, fmt.Errorf("invalid frontmatter: %w", err)
			}
			skill := protocol.Skill{Name: strings.TrimSpace(metadata.Name), Description: strings.TrimSpace(metadata.Description), Body: body}
			return skill, skill.Validate()
		}
		if !hasNewline {
			break
		}
		offset += len(line) + 1
	}
	return protocol.Skill{}, fmt.Errorf("missing closing frontmatter delimiter")
}
