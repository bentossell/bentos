package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type HomeConfig struct {
	Widgets []WidgetConfig `yaml:"widgets"`
}

type WidgetConfig struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Type    string   `yaml:"type"`
	Source  string   `yaml:"source"`
	Surface string   `yaml:"surface"`
	Path    string   `yaml:"path"`
	Query   string   `yaml:"query"`
	Columns []string `yaml:"columns"`
	Format  string   `yaml:"format"`
	MaxRows int      `yaml:"max_rows"`
}

func ReadHomeConfig(path string) (HomeConfig, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return HomeConfig{}, "", err
	}
	parsed, err := ParseFrontmatterMarkdown(string(b))
	if err != nil {
		return HomeConfig{}, "", err
	}

	var cfg HomeConfig
	fmb, err := yaml.Marshal(parsed.Frontmatter)
	if err != nil {
		return HomeConfig{}, "", err
	}
	if err := yaml.Unmarshal(fmb, &cfg); err != nil {
		return HomeConfig{}, "", err
	}
	return cfg, parsed.Body, nil
}
