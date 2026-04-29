package cmd

import "strings"

type cliFlagSpec struct {
	kind        flagKind
	noOptDefVal bool
}

type parsedPreCommandFlag struct {
	bit    CommonFlagSet
	tokens []string
}

// normalizePreCommandFlags moves supported Tier 1 common flags that appear
// before an eligible root command to immediately after that command. Cobra only
// accepts local command flags after the command name; this preserves the
// existing flag bindings while allowing invocations such as:
//
//	term-llm -p openai --yolo chat
//
// Tier 1 is intentionally limited to LLM-agent setup flags such as --provider,
// --yolo, tool/search/MCP flags, --debug, --system-message, and --skills.
// Context/control flags such as --file, --agent, --resume, --max-turns, --text,
// --json, and --fast remain command-local and are not normalized before the
// command.
func normalizePreCommandFlags(args []string) []string {
	if len(args) == 0 {
		return args
	}

	var rootPrefix []string
	var moved []parsedPreCommandFlag
	commandIndex := -1
	commandName := ""

	for i := 0; i < len(args); {
		token := args[i]
		if token == "--" {
			return args
		}

		if canonicalName, ok := canonicalRootCommandName(token); ok {
			commandIndex = i
			commandName = canonicalName
			break
		}

		if meta, ok := matchPreCommandCommonFlag(token); ok {
			consumed, ok := consumeFlagTokens(args, i, meta.Kind, false)
			if !ok {
				return args
			}
			moved = append(moved, parsedPreCommandFlag{
				bit:    meta.Bit,
				tokens: args[i : i+consumed],
			})
			i += consumed
			continue
		}

		if spec, ok := matchRootPersistentFlag(token); ok {
			consumed, ok := consumeFlagTokens(args, i, spec.kind, spec.noOptDefVal)
			if !ok {
				return args
			}
			rootPrefix = append(rootPrefix, args[i:i+consumed]...)
			i += consumed
			continue
		}

		// Unknown pre-command token. Leave the argv untouched so Cobra reports the
		// same unknown flag/command it would have reported before normalization.
		return args
	}

	if commandIndex < 0 || len(moved) == 0 {
		return args
	}

	set, ok := commandCommonFlagSets[commandName]
	if !ok {
		return args
	}
	for _, flag := range moved {
		if !set.has(flag.bit) {
			return args
		}
	}

	normalized := make([]string, 0, len(args))
	normalized = append(normalized, rootPrefix...)
	normalized = append(normalized, args[commandIndex])
	for _, flag := range moved {
		normalized = append(normalized, flag.tokens...)
	}
	normalized = append(normalized, args[commandIndex+1:]...)
	return normalized
}

func matchPreCommandCommonFlag(token string) (commonFlagMeta, bool) {
	name, shorthand, ok := splitFlagToken(token)
	if !ok {
		return commonFlagMeta{}, false
	}

	for _, meta := range commonFlagMetas {
		if !meta.PreCommand {
			continue
		}
		if name != "" && meta.Name == name {
			return meta, true
		}
		if shorthand != "" && meta.Shorthand == shorthand {
			return meta, true
		}
	}
	return commonFlagMeta{}, false
}

func matchRootPersistentFlag(token string) (cliFlagSpec, bool) {
	name, shorthand, ok := splitFlagToken(token)
	if !ok {
		return cliFlagSpec{}, false
	}

	flags := rootCmd.PersistentFlags()
	if name != "" {
		if flag := flags.Lookup(name); flag != nil {
			return cliFlagSpecFromPFlag(flag.Value.Type(), flag.NoOptDefVal != ""), true
		}
	}
	if shorthand != "" {
		if flag := flags.ShorthandLookup(shorthand); flag != nil {
			return cliFlagSpecFromPFlag(flag.Value.Type(), flag.NoOptDefVal != ""), true
		}
	}
	return cliFlagSpec{}, false
}

func splitFlagToken(token string) (name, shorthand string, ok bool) {
	if strings.HasPrefix(token, "--") && len(token) > 2 {
		body := token[2:]
		if idx := strings.Index(body, "="); idx >= 0 {
			body = body[:idx]
		}
		if body == "" {
			return "", "", false
		}
		return body, "", true
	}

	if strings.HasPrefix(token, "-") && len(token) > 1 {
		body := token[1:]
		if idx := strings.Index(body, "="); idx >= 0 {
			body = body[:idx]
		}
		// Short clusters and attached values (for example -sd or -popenai) are
		// intentionally unsupported in the pre-command position for now.
		if len(body) != 1 {
			return "", "", false
		}
		return "", body, true
	}

	return "", "", false
}

func cliFlagSpecFromPFlag(valueType string, noOptDefVal bool) cliFlagSpec {
	kind := flagKindString
	switch valueType {
	case "bool":
		kind = flagKindBool
	case "stringArray", "stringSlice":
		kind = flagKindStringArray
	}
	return cliFlagSpec{kind: kind, noOptDefVal: noOptDefVal}
}

func consumeFlagTokens(args []string, index int, kind flagKind, noOptDefVal bool) (int, bool) {
	if index < 0 || index >= len(args) {
		return 0, false
	}
	if flagTokenHasInlineValue(args[index]) || kind == flagKindBool || noOptDefVal {
		return 1, true
	}
	if index+1 >= len(args) {
		return 0, false
	}
	return 2, true
}

func flagTokenHasInlineValue(token string) bool {
	return strings.Contains(token, "=") && strings.HasPrefix(token, "-") && token != "--"
}

func isRootCommandName(token string) bool {
	_, ok := canonicalRootCommandName(token)
	return ok
}

func canonicalRootCommandName(token string) (string, bool) {
	if token == "" || strings.HasPrefix(token, "-") {
		return "", false
	}
	for _, command := range rootCmd.Commands() {
		if command.Name() == token {
			return command.Name(), true
		}
		for _, alias := range command.Aliases {
			if alias == token {
				return command.Name(), true
			}
		}
	}
	return "", false
}
