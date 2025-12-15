package query

import (
	"testing"
	"time"
)

func TestFilterBasic(t *testing.T) {
	items := []any{
		map[string]any{"a": true, "b": "x"},
		map[string]any{"a": false, "b": "x"},
		map[string]any{"a": true, "b": "y"},
	}
	out, err := Filter(items, "a == true && b == 'x'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out))
	}
}

func TestFilterTimeFunctions(t *testing.T) {
	now := time.Now().UTC()
	items := []any{map[string]any{"ts": now.Format(time.RFC3339)}}
	out, err := Filter(items, "ts > now() - 24h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out))
	}
}
