package prompt

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
)

// Template represents a prompt template using Go's text/template engine.
type Template struct {
	tmpl *template.Template
}

// New creates a new prompt template.
func New(name, tmplStr string) (*Template, error) {
	t, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	return &Template{tmpl: t}, nil
}

// Format formats the template with the provided data.
func (t *Template) Format(data any) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}
	return buf.String(), nil
}

// FormatMessages formats the template as a UserMessage.
func (t *Template) FormatMessages(data any) ([]model.Message, error) {
	text, err := t.Format(data)
	if err != nil {
		return nil, err
	}
	return []model.Message{model.UserMessage(text)}, nil
}

// Builder helps incrementally build system and user prompts.
type Builder struct {
	sections []string
}

// NewBuilder creates a new prompt builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// AddSection appends a section title and body.
func (b *Builder) AddSection(title, content string) *Builder {
	if strings.TrimSpace(content) == "" {
		return b
	}
	section := fmt.Sprintf("## %s\n\n%s", title, strings.TrimSpace(content))
	b.sections = append(b.sections, section)
	return b
}

// AddText appends raw text to the prompt.
func (b *Builder) AddText(text string) *Builder {
	if strings.TrimSpace(text) == "" {
		return b
	}
	b.sections = append(b.sections, strings.TrimSpace(text))
	return b
}

// Build combines all sections into a single string.
func (b *Builder) Build() string {
	return strings.Join(b.sections, "\n\n")
}
