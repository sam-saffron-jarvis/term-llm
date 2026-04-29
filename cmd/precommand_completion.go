package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func isShellCompletionRequest(arg string) bool {
	return arg == cobra.ShellCompRequestCmd || arg == cobra.ShellCompNoDescRequestCmd
}

func normalizeShellCompletionArgs(args []string) []string {
	if len(args) == 0 || !isShellCompletionRequest(args[0]) {
		return normalizePreCommandFlags(args)
	}

	normalized := normalizePreCommandFlags(args[1:])
	result := make([]string, 0, len(args))
	result = append(result, args[0])
	result = append(result, normalized...)
	return result
}

func handlePreCommandCompletion(args []string) bool {
	if len(args) < 2 || !isShellCompletionRequest(args[0]) {
		return false
	}

	completionArgs := args[1:]
	toComplete := completionArgs[len(completionArgs)-1]
	noDesc := args[0] == cobra.ShellCompNoDescRequestCmd

	if handlePreCommandFlagValueCompletion(completionArgs, toComplete, noDesc) {
		return true
	}

	bits, ok := parsePreCommandCompletionPrefix(completionArgs[:len(completionArgs)-1])
	if !ok {
		return false
	}

	if toComplete != "" && strings.HasPrefix(toComplete, "-") && !strings.Contains(toComplete, "=") {
		completions := rootPreCommandFlagNameCompletions(toComplete, noDesc, bits)
		printShellCompletions(completions, cobra.ShellCompDirectiveNoFileComp)
		return true
	}

	// Once a pre-command flag has been entered, Cobra cannot parse the line on its
	// own because the command has not appeared yet. Complete only commands that
	// support all pre-command flags already present.
	if bits != 0 && !strings.HasPrefix(toComplete, "-") {
		completions := preCommandRootCommandCompletions(toComplete, noDesc, bits)
		printShellCompletions(completions, cobra.ShellCompDirectiveNoFileComp)
		return true
	}

	return false
}

func handlePreCommandFlagValueCompletion(completionArgs []string, toComplete string, noDesc bool) bool {
	prefixArgs := completionArgs[:len(completionArgs)-1]

	if strings.HasPrefix(toComplete, "-") && strings.Contains(toComplete, "=") {
		meta, ok := matchPreCommandCommonFlag(toComplete)
		if !ok || meta.Kind == flagKindBool {
			return false
		}
		bits, ok := parsePreCommandCompletionPrefix(prefixArgs)
		if !ok || !anyCommandSupports(bits|meta.Bit) {
			return false
		}
		value := toComplete[strings.Index(toComplete, "=")+1:]
		completions, directive := preCommandFlagValueCompletions(meta, value)
		if noDesc {
			completions = stripCompletionDescriptions(completions)
		}
		printShellCompletions(completions, directive)
		return true
	}

	if len(prefixArgs) == 0 {
		return false
	}
	prev := prefixArgs[len(prefixArgs)-1]
	meta, ok := matchPreCommandCommonFlag(prev)
	if !ok || meta.Kind == flagKindBool || flagTokenHasInlineValue(prev) {
		return false
	}
	bits, ok := parsePreCommandCompletionPrefix(prefixArgs[:len(prefixArgs)-1])
	if !ok || !anyCommandSupports(bits|meta.Bit) {
		return false
	}
	completions, directive := preCommandFlagValueCompletions(meta, toComplete)
	if noDesc {
		completions = stripCompletionDescriptions(completions)
	}
	printShellCompletions(completions, directive)
	return true
}

func parsePreCommandCompletionPrefix(args []string) (CommonFlagSet, bool) {
	var bits CommonFlagSet
	for i := 0; i < len(args); {
		token := args[i]
		if token == "--" || isRootCommandName(token) {
			return 0, false
		}
		if meta, ok := matchPreCommandCommonFlag(token); ok {
			consumed, ok := consumeFlagTokens(args, i, meta.Kind, false)
			if !ok {
				return 0, false
			}
			bits |= meta.Bit
			i += consumed
			continue
		}
		if spec, ok := matchRootPersistentFlag(token); ok {
			consumed, ok := consumeFlagTokens(args, i, spec.kind, spec.noOptDefVal)
			if !ok {
				return 0, false
			}
			i += consumed
			continue
		}
		return 0, false
	}
	return bits, true
}

func rootPreCommandFlagNameCompletions(toComplete string, noDesc bool, existingBits CommonFlagSet) []string {
	// Cobra normally initializes these during completion execution. Since this
	// custom path bypasses Cobra's __complete command, initialize them here so root
	// completion remains a superset of the default root flags.
	rootCmd.InitDefaultHelpFlag()
	rootCmd.InitDefaultVersionFlag()

	seen := map[string]bool{}
	var completions []string
	add := func(name, usage string) {
		if name == "" || seen[name] || !strings.HasPrefix(name, toComplete) {
			return
		}
		seen[name] = true
		if noDesc || usage == "" {
			completions = append(completions, name)
			return
		}
		completions = append(completions, name+"\t"+usage)
	}
	addPFlag := func(flag *pflag.Flag) {
		add("--"+flag.Name, flag.Usage)
		if flag.Shorthand != "" {
			add("-"+flag.Shorthand, flag.Usage)
		}
	}

	rootCmd.PersistentFlags().VisitAll(addPFlag)
	rootCmd.Flags().VisitAll(addPFlag)

	for _, meta := range commonFlagMetas {
		if !meta.PreCommand || !anyCommandSupports(existingBits|meta.Bit) {
			continue
		}
		usage := ""
		shorthand := meta.Shorthand
		if flag := findCommonFlag(meta); flag != nil {
			usage = flag.Usage
			shorthand = flag.Shorthand
		}
		add("--"+meta.Name, usage)
		if shorthand != "" {
			add("-"+shorthand, usage)
		}
	}

	return completions
}

func preCommandFlagValueCompletions(meta commonFlagMeta, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch meta.Name {
	case "provider":
		return ProviderFlagCompletion(rootCmd, nil, toComplete)
	case "mcp":
		return MCPFlagCompletion(rootCmd, nil, toComplete)
	case "tools":
		return ToolsFlagCompletion(rootCmd, nil, toComplete)
	case "skills":
		return SkillsFlagCompletion(rootCmd, nil, toComplete)
	case "read-dir", "write-dir":
		return nil, cobra.ShellCompDirectiveFilterDirs
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func preCommandRootCommandCompletions(toComplete string, noDesc bool, bits CommonFlagSet) []string {
	var completions []string
	add := func(name, short string) {
		if !strings.HasPrefix(name, toComplete) {
			return
		}
		if noDesc || short == "" {
			completions = append(completions, name)
		} else {
			completions = append(completions, name+"\t"+short)
		}
	}

	for _, command := range rootCmd.Commands() {
		if !command.IsAvailableCommand() {
			continue
		}
		set, ok := commandCommonFlagSets[command.Name()]
		if !ok || set&bits != bits {
			continue
		}
		add(command.Name(), command.Short)
		for _, alias := range command.Aliases {
			add(alias, command.Short)
		}
	}
	return completions
}

func anyCommandSupports(bits CommonFlagSet) bool {
	if bits == 0 {
		return true
	}
	for _, set := range commandCommonFlagSets {
		if set&bits == bits {
			return true
		}
	}
	return false
}

func findCommonFlag(meta commonFlagMeta) *pflag.Flag {
	for _, command := range rootCmd.Commands() {
		set, ok := commandCommonFlagSets[command.Name()]
		if !ok || !set.has(meta.Bit) {
			continue
		}
		if flag := command.Flags().Lookup(meta.Name); flag != nil {
			return flag
		}
	}
	return nil
}

func stripCompletionDescriptions(completions []string) []string {
	stripped := make([]string, 0, len(completions))
	for _, completion := range completions {
		stripped = append(stripped, strings.SplitN(completion, "\t", 2)[0])
	}
	return stripped
}

func printShellCompletions(completions []string, directive cobra.ShellCompDirective) {
	out := rootCmd.OutOrStdout()
	for _, completion := range completions {
		completion = strings.SplitN(completion, "\n", 2)[0]
		completion = strings.TrimSpace(completion)
		fmt.Fprintln(out, completion)
	}
	fmt.Fprintf(out, ":%d\n", directive)
	fmt.Fprintf(rootCmd.ErrOrStderr(), "Completion ended with directive: %d\n", directive)
}
