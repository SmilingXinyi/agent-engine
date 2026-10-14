package protocol

import (
	"errors"
	"strings"
)

func (skill Skill) Validate() error {
	if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Name) != skill.Name || strings.ContainsAny(skill.Name, "\r\n") {
		return errors.New("skill name must be non-empty without surrounding whitespace or line breaks")
	}
	if strings.TrimSpace(skill.Description) == "" {
		return errors.New("skill description is required")
	}
	if strings.TrimSpace(skill.Body) == "" {
		return errors.New("skill instructions are required")
	}
	return nil
}
