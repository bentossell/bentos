package config

import (
	"bytes"
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

type ParsedMarkdown struct {
	Frontmatter map[string]any
	Body        string
}

func ParseFrontmatterMarkdown(raw string) (ParsedMarkdown, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ParsedMarkdown{Frontmatter: map[string]any{}, Body: raw}, nil
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return ParsedMarkdown{}, errors.New("frontmatter: missing closing ---")
	}

	yml := strings.Join(lines[1:end], "\n")
	var fm map[string]any
	dec := yaml.NewDecoder(bytes.NewBufferString(yml))
	dec.KnownFields(false)
	if err := dec.Decode(&fm); err != nil {
		return ParsedMarkdown{}, err
	}
	if fm == nil {
		fm = map[string]any{}
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return ParsedMarkdown{Frontmatter: fm, Body: body}, nil
}
