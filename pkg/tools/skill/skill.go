package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	maxMatchedSkills    = 5
	nativeSkillFileName = "SKILL.md"
)

type SkillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type Skill struct {
	Name     string
	Dir      string
	Path     string
	Metadata SkillMetadata
	Body     string
	Score    int
}

func LoadSkills(skillsRoot string) ([]Skill, error) {
	info, err := os.Stat(skillsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills path is not a directory: %s", skillsRoot)
	}

	skillDirs, err := discoverSkillDirs(skillsRoot)
	if err != nil {
		return nil, err
	}
	skills := make([]Skill, 0, len(skillDirs))
	for _, dir := range skillDirs {
		skill, err := loadSkill(dir)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	sortSkillsByName(skills)
	return skills, nil
}

func MatchSkills(userPrompt string, skills []Skill) []Skill {
	prompt := strings.ToLower(userPrompt)
	matched := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		score := scoreSkill(prompt, skill)
		if score <= 0 {
			continue
		}
		skill.Score = score
		matched = append(matched, skill)
	}
	sort.Slice(matched, func(firstIndex, secondIndex int) bool {
		if matched[firstIndex].Score != matched[secondIndex].Score {
			return matched[firstIndex].Score > matched[secondIndex].Score
		}
		if matched[firstIndex].Name != matched[secondIndex].Name {
			return matched[firstIndex].Name < matched[secondIndex].Name
		}
		return matched[firstIndex].Dir < matched[secondIndex].Dir
	})
	if len(matched) > maxMatchedSkills {
		matched = matched[:maxMatchedSkills]
	}
	return matched
}

func SelectSkills(userPrompt string, skills []Skill) []Skill {
	return MatchSkills(userPrompt, skills)
}

func discoverSkillDirs(skillsRoot string) ([]string, error) {
	var dirs []string
	if err := filepath.WalkDir(skillsRoot, func(path string, dirEntry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !dirEntry.IsDir() {
			return nil
		}
		if path != skillsRoot && hasNativeSkillFile(path) {
			dirs = append(dirs, path)
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func loadSkill(dir string) (Skill, error) {
	path := filepath.Join(dir, nativeSkillFileName)
	metadata, body, err := readNativeSkill(path)
	if err != nil {
		return Skill{}, err
	}
	return Skill{Name: metadata.Name, Dir: dir, Path: path, Metadata: metadata, Body: body}, nil
}

func readNativeSkill(path string) (SkillMetadata, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return SkillMetadata{}, "", fmt.Errorf("read skill file %s: %w", path, err)
	}
	metadata, body, err := parseNativeSkill(content)
	if err != nil {
		return SkillMetadata{}, "", fmt.Errorf("parse skill frontmatter %s: %w", path, err)
	}
	if strings.TrimSpace(metadata.Name) == "" {
		return SkillMetadata{}, "", fmt.Errorf("skill %s missing required frontmatter field name", path)
	}
	if strings.TrimSpace(metadata.Description) == "" {
		return SkillMetadata{}, "", fmt.Errorf("skill %s missing required frontmatter field description", path)
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	return metadata, body, nil
}

func parseNativeSkill(content []byte) (SkillMetadata, string, error) {
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(content, []byte("---\n")) && !bytes.HasPrefix(content, []byte("---\r\n")) {
		return SkillMetadata{}, "", fmt.Errorf("missing opening frontmatter delimiter")
	}

	lineStart := 0
	for lineStart < len(content) {
		lineEnd := lineStart
		for lineEnd < len(content) && content[lineEnd] != '\n' {
			lineEnd++
		}
		line := bytes.TrimSpace(content[lineStart:lineEnd])
		if lineStart > 0 && bytes.Equal(line, []byte("---")) {
			bodyStart := lineEnd
			if bodyStart < len(content) && content[bodyStart] == '\n' {
				bodyStart++
			}
			frontmatter := content[firstLineEnd(content):lineStart]
			var metadata SkillMetadata
			if err := yaml.Unmarshal(frontmatter, &metadata); err != nil {
				return SkillMetadata{}, "", err
			}
			return metadata, string(content[bodyStart:]), nil
		}
		lineStart = lineEnd
		if lineStart < len(content) && content[lineStart] == '\n' {
			lineStart++
		}
	}
	return SkillMetadata{}, "", fmt.Errorf("missing closing frontmatter delimiter")
}

func firstLineEnd(content []byte) int {
	for index, char := range content {
		if char == '\n' {
			return index + 1
		}
	}
	return len(content)
}

func scoreSkill(prompt string, skill Skill) int {
	score := 0
	if containsNormalized(prompt, skill.Name) {
		score += 50
	}
	for _, term := range descriptionTerms(skill.Metadata.Description) {
		if containsNormalized(prompt, term) {
			score += 10
			if score >= 100 {
				return score
			}
		}
	}
	return score
}

func descriptionTerms(description string) []string {
	fields := strings.FieldsFunc(strings.ToLower(description), func(value rune) bool {
		return unicode.IsSpace(value) || strings.ContainsRune(",.;:!?()[]{}<>/\\\"'`，。；：？！、（）【】《》|", value)
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		term := strings.TrimSpace(field)
		if !isMeaningfulDescriptionTerm(term) {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func isMeaningfulDescriptionTerm(term string) bool {
	if term == "" {
		return false
	}
	if len([]rune(term)) <= 1 {
		return false
	}
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "as": {}, "for": {}, "in": {}, "is": {}, "it": {}, "of": {}, "on": {}, "or": {}, "the": {}, "this": {}, "to": {}, "use": {}, "user": {}, "when": {}, "whenever": {}, "with": {}, "wants": {}, "skill": {},
	}
	_, ignored := stopwords[term]
	return !ignored
}

func containsNormalized(prompt, value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value != "" && strings.Contains(prompt, value)
}

func hasNativeSkillFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, nativeSkillFileName))
	return err == nil && info.Mode().IsRegular()
}

func sortSkillsByName(skills []Skill) {
	sort.Slice(skills, func(firstIndex, secondIndex int) bool {
		if skills[firstIndex].Name != skills[secondIndex].Name {
			return skills[firstIndex].Name < skills[secondIndex].Name
		}
		return skills[firstIndex].Dir < skills[secondIndex].Dir
	})
}
