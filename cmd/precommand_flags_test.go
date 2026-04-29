package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNormalizePreCommandFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "short provider and yolo before chat",
			args: []string{"-p", "openai", "--yolo", "chat"},
			want: []string{"chat", "-p", "openai", "--yolo"},
		},
		{
			name: "long provider with inline value",
			args: []string{"--provider=openai", "chat"},
			want: []string{"chat", "--provider=openai"},
		},
		{
			name: "tool flags preserve repeat order",
			args: []string{"--tools", "all", "--read-dir", "a", "--read-dir", "b", "chat"},
			want: []string{"chat", "--tools", "all", "--read-dir", "a", "--read-dir", "b"},
		},
		{
			name: "root persistent flag stays before command",
			args: []string{"--stats", "-p", "openai", "chat"},
			want: []string{"--stats", "chat", "-p", "openai"},
		},
		{
			name: "root persistent string flag keeps value",
			args: []string{"--session-db", "sessions.db", "-p", "openai", "chat"},
			want: []string{"--session-db", "sessions.db", "chat", "-p", "openai"},
		},
		{
			name: "root persistent optional value flag without value",
			args: []string{"--pprof", "-p", "openai", "chat"},
			want: []string{"--pprof", "chat", "-p", "openai"},
		},
		{
			name: "system message before ask keeps positional args",
			args: []string{"--system-message", "Be terse", "ask", "hello"},
			want: []string{"ask", "--system-message", "Be terse", "hello"},
		},
		{
			name: "short system message before chat",
			args: []string{"-m", "Be terse", "chat"},
			want: []string{"chat", "-m", "Be terse"},
		},
		{
			name: "skills supported by chat",
			args: []string{"--skills", "all", "chat"},
			want: []string{"chat", "--skills", "all"},
		},
		{
			name: "search unsupported by edit remains unchanged",
			args: []string{"--search", "edit", "fix"},
			want: []string{"--search", "edit", "fix"},
		},
		{
			name: "no web fetch unsupported by serve remains unchanged",
			args: []string{"--no-web-fetch", "serve", "web"},
			want: []string{"--no-web-fetch", "serve", "web"},
		},
		{
			name: "terminator stops normalization",
			args: []string{"--", "-p", "openai", "chat"},
			want: []string{"--", "-p", "openai", "chat"},
		},
		{
			name: "unknown pre-command flag remains unchanged",
			args: []string{"--unknown", "chat"},
			want: []string{"--unknown", "chat"},
		},
		{
			name: "short clusters remain unchanged",
			args: []string{"-sd", "chat"},
			want: []string{"-sd", "chat"},
		},
		{
			name: "non tier one file flag remains unchanged",
			args: []string{"-f", "main.go", "ask", "explain"},
			want: []string{"-f", "main.go", "ask", "explain"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePreCommandFlags(append([]string(nil), tc.args...))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizePreCommandFlags(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestNormalizeShellCompletionArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "completion args normalize pre-command flags before command",
			args: []string{"__complete", "-p", "openai", "--yolo", "chat", "-"},
			want: []string{"__complete", "chat", "-p", "openai", "--yolo", "-"},
		},
		{
			name: "root flag completion without command remains for custom handler",
			args: []string{"__complete", "-"},
			want: []string{"__complete", "-"},
		},
		{
			name: "no description completion command also normalizes",
			args: []string{"__completeNoDesc", "--provider=openai", "chat", "-"},
			want: []string{"__completeNoDesc", "chat", "--provider=openai", "-"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeShellCompletionArgs(append([]string(nil), tc.args...))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeShellCompletionArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestRootPreCommandFlagNameCompletions(t *testing.T) {
	completions := rootPreCommandFlagNameCompletions("-", false, 0)
	joined := strings.Join(completions, "\n")
	for _, want := range []string{
		"--provider\t",
		"-p\t",
		"--yolo\t",
		"--tools\t",
		"--read-dir\t",
		"--mcp\t",
		"--search\t",
		"-s\t",
		"--debug\t",
		"-d\t",
		"--system-message\t",
		"-m\t",
		"--skills\t",
		"--stats\t",
		"--session-db\t",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected completion containing %q in:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"--agent\t", "-a\t", "--file\t", "-f\t", "--max-turns\t", "--text\t", "--json\t"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("unexpected completion containing %q in:\n%s", unwanted, joined)
		}
	}
}

func TestPreCommandRootCommandCompletionsAreCommandAware(t *testing.T) {
	withSearch := strings.Join(preCommandRootCommandCompletions("", false, CommonSearch), "\n")
	for _, want := range []string{"ask\t", "chat\t", "exec\t", "loop\t", "serve\t"} {
		if !strings.Contains(withSearch, want) {
			t.Fatalf("expected search completion containing %q in:\n%s", want, withSearch)
		}
	}
	if strings.Contains(withSearch, "edit\t") {
		t.Fatalf("did not expect edit for search completion in:\n%s", withSearch)
	}

	withNoWebFetch := strings.Join(preCommandRootCommandCompletions("", false, CommonNoWebFetch), "\n")
	if strings.Contains(withNoWebFetch, "serve\t") {
		t.Fatalf("did not expect serve for no-web-fetch completion in:\n%s", withNoWebFetch)
	}
}

func TestNormalizePreCommandFlagsUsesCanonicalCommandForAliases(t *testing.T) {
	const alias = "chat-alias-for-test"
	oldAliases := chatCmd.Aliases
	chatCmd.Aliases = append(append([]string(nil), oldAliases...), alias)
	t.Cleanup(func() { chatCmd.Aliases = oldAliases })

	got := normalizePreCommandFlags([]string{"-p", "openai", alias})
	want := []string{alias, "-p", "openai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizePreCommandFlags with alias = %v, want %v", got, want)
	}

	completions := strings.Join(preCommandRootCommandCompletions("chat-alias", true, CommonProvider), "\n")
	if !strings.Contains(completions, alias) {
		t.Fatalf("expected alias completion %q in:\n%s", alias, completions)
	}
}

func TestPreCommandNormalizationParsesWithCobra(t *testing.T) {
	t.Run("chat provider yolo", func(t *testing.T) {
		cleanupPreCommandParseState(t)
		normalized := normalizePreCommandFlags([]string{"-p", "openai", "--yolo", "chat"})
		cmd, positional, err := parseNormalizedArgsWithCobraForTest(normalized)
		if err != nil {
			t.Fatalf("parse normalized args error: %v", err)
		}
		if cmd.Name() != "chat" {
			t.Fatalf("command = %q, want chat", cmd.Name())
		}
		if got := cmd.Flags().Lookup("provider").Value.String(); got != "openai" {
			t.Fatalf("provider = %q, want openai", got)
		}
		if !cmd.Flags().Changed("yolo") {
			t.Fatal("expected --yolo to be marked changed")
		}
		if len(positional) != 0 {
			t.Fatalf("positional args = %v, want none", positional)
		}
	})

	t.Run("root stats ask provider positional", func(t *testing.T) {
		cleanupPreCommandParseState(t)
		normalized := normalizePreCommandFlags([]string{"--stats", "-p", "openai", "ask", "hello"})
		cmd, positional, err := parseNormalizedArgsWithCobraForTest(normalized)
		if err != nil {
			t.Fatalf("parse normalized args error: %v", err)
		}
		if cmd.Name() != "ask" {
			t.Fatalf("command = %q, want ask", cmd.Name())
		}
		if !showStats {
			t.Fatal("expected root --stats to set showStats")
		}
		if got := cmd.Flags().Lookup("provider").Value.String(); got != "openai" {
			t.Fatalf("provider = %q, want openai", got)
		}
		if !reflect.DeepEqual(positional, []string{"hello"}) {
			t.Fatalf("positional args = %v, want [hello]", positional)
		}
	})

	t.Run("unsupported no web fetch before serve is rejected", func(t *testing.T) {
		cleanupPreCommandParseState(t)
		input := []string{"--no-web-fetch", "serve", "web"}
		normalized := normalizePreCommandFlags(input)
		if !reflect.DeepEqual(normalized, input) {
			t.Fatalf("normalizePreCommandFlags(%v) = %v, want unchanged", input, normalized)
		}
		_, _, err := parseNormalizedArgsWithCobraForTest(normalized)
		if err == nil || !strings.Contains(err.Error(), "unknown flag: --no-web-fetch") {
			t.Fatalf("parse error = %v, want unknown --no-web-fetch", err)
		}
	})
}

func parseNormalizedArgsWithCobraForTest(args []string) (*cobra.Command, []string, error) {
	cmd, remaining, err := rootCmd.Traverse(args)
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.ParseFlags(remaining); err != nil {
		return cmd, nil, err
	}
	return cmd, cmd.Flags().Args(), nil
}

func cleanupPreCommandParseState(t *testing.T) {
	t.Helper()

	oldShowStats := showStats
	oldAskProvider := askProvider
	oldChatProvider := chatProvider
	oldChatYolo := chatYolo

	changed := map[*pflag.Flag]bool{}
	capture := func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			changed[flag] = flag.Changed
		})
		cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
			changed[flag] = flag.Changed
		})
	}
	capture(rootCmd)
	capture(askCmd)
	capture(chatCmd)
	capture(serveCmd)

	t.Cleanup(func() {
		showStats = oldShowStats
		askProvider = oldAskProvider
		chatProvider = oldChatProvider
		chatYolo = oldChatYolo
		for flag, wasChanged := range changed {
			flag.Changed = wasChanged
		}
	})
}
