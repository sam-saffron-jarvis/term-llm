package cmd

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/tools"
)

func TestParseMCPToolsFlag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "all expands to full list",
			input:    "all",
			expected: mcpAllToolNames(),
		},
		{
			name:     "star expands to full list",
			input:    "*",
			expected: mcpAllToolNames(),
		},
		{
			name:     "single tool",
			input:    "read_file",
			expected: []string{"read_file"},
		},
		{
			name:     "comma separated",
			input:    "read_file,grep,glob",
			expected: []string{"read_file", "grep", "glob"},
		},
		{
			name:     "deduplication",
			input:    "read_file,grep,read_file",
			expected: []string{"read_file", "grep"},
		},
		{
			name:     "whitespace trimming",
			input:    " read_file , grep ",
			expected: []string{"read_file", "grep"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "all with whitespace",
			input:    " all ",
			expected: mcpAllToolNames(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseMCPToolsFlag(tc.input)
			if len(result) != len(tc.expected) {
				t.Fatalf("parseMCPToolsFlag(%q) returned %d items, want %d\ngot:  %v\nwant: %v",
					tc.input, len(result), len(tc.expected), result, tc.expected)
			}
			for i := range result {
				if result[i] != tc.expected[i] {
					t.Errorf("parseMCPToolsFlag(%q)[%d] = %q, want %q", tc.input, i, result[i], tc.expected[i])
				}
			}
		})
	}
}

func TestValidateMCPToolNames(t *testing.T) {
	t.Run("valid tools pass", func(t *testing.T) {
		err := validateMCPToolNames([]string{"read_file", "grep", "web_search"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unified_diff passes (allowed for edit-format swap)", func(t *testing.T) {
		err := validateMCPToolNames([]string{"unified_diff"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ask_user rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"read_file", "ask_user"})
		if err == nil {
			t.Fatal("expected error for ask_user")
		}
		if got := err.Error(); !strings.Contains(got, "ask_user") {
			t.Errorf("error should mention ask_user, got: %s", got)
		}
	})

	t.Run("spawn_agent rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"spawn_agent"})
		if err == nil {
			t.Fatal("expected error for spawn_agent")
		}
	})

	t.Run("run_agent_script rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"run_agent_script"})
		if err == nil {
			t.Fatal("expected error for run_agent_script")
		}
	})

	t.Run("view_image rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"view_image"})
		if err == nil {
			t.Fatal("expected error for view_image")
		}
	})

	t.Run("show_image rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"show_image"})
		if err == nil {
			t.Fatal("expected error for show_image")
		}
	})

	t.Run("totally unknown name rejected", func(t *testing.T) {
		err := validateMCPToolNames([]string{"nonexistent"})
		if err == nil {
			t.Fatal("expected error for nonexistent tool")
		}
	})

	t.Run("multiple invalid reported together", func(t *testing.T) {
		err := validateMCPToolNames([]string{"ask_user", "spawn_agent"})
		if err == nil {
			t.Fatal("expected error")
		}
		got := err.Error()
		if !strings.Contains(got, "ask_user") || !strings.Contains(got, "spawn_agent") {
			t.Errorf("error should list both invalid tools, got: %s", got)
		}
	})

	t.Run("empty list passes", func(t *testing.T) {
		err := validateMCPToolNames(nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestSwapEditTool(t *testing.T) {
	t.Run("swaps edit_file to unified_diff", func(t *testing.T) {
		input := []string{"read_file", tools.EditFileToolName, "grep"}
		result := swapEditTool(input, tools.EditFileToolName, tools.UnifiedDiffToolName)
		expected := []string{"read_file", tools.UnifiedDiffToolName, "grep"}
		assertStringSlice(t, result, expected)
	})

	t.Run("no-op when target not present", func(t *testing.T) {
		input := []string{"read_file", "grep"}
		result := swapEditTool(input, tools.EditFileToolName, tools.UnifiedDiffToolName)
		assertStringSlice(t, result, input)
	})

	t.Run("does not modify original", func(t *testing.T) {
		input := []string{tools.EditFileToolName}
		result := swapEditTool(input, tools.EditFileToolName, tools.UnifiedDiffToolName)
		if input[0] != tools.EditFileToolName {
			t.Error("original slice was mutated")
		}
		if result[0] != tools.UnifiedDiffToolName {
			t.Errorf("expected %s, got %s", tools.UnifiedDiffToolName, result[0])
		}
	})
}

func TestMCPAllToolNamesAreValid(t *testing.T) {
	// Ensure every name in the "all" list is in the allowlist.
	for _, name := range mcpAllToolNames() {
		if !mcpAllowedTools[name] {
			t.Errorf("tool %q is in mcpAllToolNames() but not in mcpAllowedTools", name)
		}
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}
