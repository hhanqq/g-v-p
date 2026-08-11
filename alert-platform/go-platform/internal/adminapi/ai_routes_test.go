package adminapi

import "testing"

func TestParseToolSelectionPlainJSON(t *testing.T) {
	parsed, err := parseToolSelection(`{"tool":"list_active_incidents","params":{"priority":"P0"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Tool != "list_active_incidents" {
		t.Fatalf("tool = %q", parsed.Tool)
	}
	if parsed.Params["priority"] != "P0" {
		t.Fatalf("params = %#v", parsed.Params)
	}
}

func TestParseToolSelectionStripsMarkdownFence(t *testing.T) {
	raw := "Конечно! ```json\n{\"tool\":\"get_coverage\",\"params\":{}}\n```"
	parsed, err := parseToolSelection(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Tool != "get_coverage" {
		t.Fatalf("tool = %q", parsed.Tool)
	}
}

func TestParseToolSelectionRejectsGarbage(t *testing.T) {
	if _, err := parseToolSelection("я не понимаю, что вы имеете в виду"); err == nil {
		t.Fatal("expected error for non-JSON model output")
	}
}

func TestParseToolSelectionRejectsEmptyTool(t *testing.T) {
	if _, err := parseToolSelection(`{"tool":"","params":{}}`); err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestFindAIToolUnknownName(t *testing.T) {
	if _, ok := findAITool("delete_everything"); ok {
		t.Fatal("unknown tool must not be found in the registry")
	}
}

func TestFindAIToolKnownNames(t *testing.T) {
	for _, name := range []string{
		"list_active_incidents", "get_incident", "find_alerts", "find_equipment",
		"get_available_responders", "get_coverage", "get_analytics", "open_entity",
	} {
		if _, ok := findAITool(name); !ok {
			t.Fatalf("expected %q to be registered", name)
		}
	}
}

func TestAIClampLimit(t *testing.T) {
	if got := aiClampLimit(0, 20); got != 20 {
		t.Fatalf("zero limit should default to max, got %d", got)
	}
	if got := aiClampLimit(500, 20); got != 20 {
		t.Fatalf("oversized limit should be capped, got %d", got)
	}
	if got := aiClampLimit(5, 20); got != 5 {
		t.Fatalf("in-range limit should pass through, got %d", got)
	}
}
