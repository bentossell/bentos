package config

import "testing"

func TestParseFrontmatterMarkdown(t *testing.T) {
	raw := "---\nfoo: bar\nnum: 2\n---\n\n# Hi\n"
	parsed, err := ParseFrontmatterMarkdown(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Frontmatter["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %#v", parsed.Frontmatter["foo"])
	}
	if parsed.Body == "" || parsed.Body[0] != '#' {
		t.Fatalf("expected body to start with '#', got %q", parsed.Body)
	}
}
