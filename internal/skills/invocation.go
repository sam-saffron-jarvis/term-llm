package skills

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SkillActivationOrigin identifies who requested a skill activation. Invocation
// controls are enforced against the origin at activation time, not just while
// building discovery catalogs.
type SkillActivationOrigin int

const (
	SkillActivationModel SkillActivationOrigin = iota
	SkillActivationUser
)

func (o SkillActivationOrigin) String() string {
	switch o {
	case SkillActivationModel:
		return "model"
	case SkillActivationUser:
		return "user"
	default:
		return "unknown"
	}
}

// SkillExecutionMode controls whether a directly invoked skill runs in the
// current conversation or a fresh isolated child agent.
type SkillExecutionMode int

const (
	SkillExecutionMain SkillExecutionMode = iota
	SkillExecutionIsolatedAgent
)

func (m SkillExecutionMode) String() string {
	switch m {
	case SkillExecutionMain:
		return "main"
	case SkillExecutionIsolatedAgent:
		return "isolated"
	default:
		return "unknown"
	}
}

// InvocationMetadata contains term-llm's Claude-compatible Agent Skills client
// extensions. These fields are intentionally decoded from Skill.Extras rather
// than being represented as normative Agent Skills frontmatter.
type InvocationMetadata struct {
	UserInvocable          bool
	DisableModelInvocation bool
	ArgumentHint           string
	Execution              SkillExecutionMode
	Agent                  string
	Model                  string
}

// InvocationFor decodes invocation-related client extensions from a skill.
// Unknown extras remain untouched.
func InvocationFor(skill *Skill) (InvocationMetadata, error) {
	metadata := InvocationMetadata{
		UserInvocable: true,
		Execution:     SkillExecutionMain,
	}
	if skill == nil {
		return metadata, fmt.Errorf("decode invocation metadata: skill is nil")
	}

	var err error
	if metadata.UserInvocable, err = invocationBool(skill.Extras, "user-invocable", true); err != nil {
		return InvocationMetadata{}, err
	}
	if metadata.DisableModelInvocation, err = invocationBool(skill.Extras, "disable-model-invocation", false); err != nil {
		return InvocationMetadata{}, err
	}
	if metadata.ArgumentHint, err = invocationString(skill.Extras, "argument-hint"); err != nil {
		return InvocationMetadata{}, err
	}

	contextValue, err := invocationString(skill.Extras, "context")
	if err != nil {
		return InvocationMetadata{}, err
	}
	switch contextValue {
	case "", "main":
		metadata.Execution = SkillExecutionMain
	case "fork":
		metadata.Execution = SkillExecutionIsolatedAgent
	default:
		return InvocationMetadata{}, fmt.Errorf("invalid context %q: expected main or fork", contextValue)
	}

	if metadata.Agent, err = invocationString(skill.Extras, "agent"); err != nil {
		return InvocationMetadata{}, err
	}
	if metadata.Model, err = invocationString(skill.Extras, "model"); err != nil {
		return InvocationMetadata{}, err
	}
	if metadata.Execution == SkillExecutionIsolatedAgent && metadata.Agent == "" {
		metadata.Agent = "developer"
	}
	return metadata, nil
}

func invocationBool(extras map[string]any, key string, defaultValue bool) (bool, error) {
	value, ok := extras[key]
	if !ok {
		return defaultValue, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("invalid %s: expected boolean, got %T", key, value)
	}
	return boolean, nil
}

func invocationString(extras map[string]any, key string) (string, error) {
	value, ok := extras[key]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid %s: expected string, got %T", key, value)
	}
	return strings.TrimSpace(text), nil
}

// ParseInvocationArguments parses positional invocation arguments using a small,
// deterministic shell-like grammar. It supports whitespace separation, single
// and double quotes, and backslash escaping, but performs no environment,
// command, tilde, or glob expansion.
func ParseInvocationArguments(raw string) ([]string, error) {
	var (
		arguments    []string
		current      strings.Builder
		quote        rune
		escaped      bool
		tokenStarted bool
	)

	flush := func() {
		if !tokenStarted {
			return
		}
		arguments = append(arguments, current.String())
		current.Reset()
		tokenStarted = false
	}

	for _, char := range raw {
		if escaped {
			current.WriteRune(char)
			tokenStarted = true
			escaped = false
			continue
		}

		if quote != 0 {
			switch {
			case quote == '\'' && char == '\'':
				quote = 0
			case quote == '"' && char == '"':
				quote = 0
			case quote == '"' && char == '\\':
				escaped = true
			default:
				current.WriteRune(char)
				tokenStarted = true
			}
			continue
		}

		switch char {
		case '\'', '"':
			quote = char
			tokenStarted = true
		case '\\':
			escaped = true
			tokenStarted = true
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteRune(char)
			tokenStarted = true
		}
	}

	if escaped {
		return nil, fmt.Errorf("unterminated escaped argument")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted argument")
	}
	flush()
	return arguments, nil
}

var invocationArgumentPattern = regexp.MustCompile(`\$ARGUMENTS(?:\[([0-9]+)\])?`)

// ExpandInvocationArguments performs one-pass replacement of $ARGUMENTS and
// zero-based $ARGUMENTS[N] placeholders. Values introduced by a replacement are
// not subsequently interpreted as placeholders. Missing positional arguments
// expand to an empty string, allowing optional argument slots in skill prompts.
func ExpandInvocationArguments(body, raw string) (string, error) {
	arguments, err := ParseInvocationArguments(raw)
	if err != nil {
		return "", err
	}

	hasPlaceholder := invocationArgumentPattern.MatchString(body)
	if !hasPlaceholder {
		if strings.TrimSpace(raw) == "" {
			return body, nil
		}
		return body + "\n\n---\n\n## Invocation arguments\n\n" + raw, nil
	}

	expanded := invocationArgumentPattern.ReplaceAllStringFunc(body, func(placeholder string) string {
		matches := invocationArgumentPattern.FindStringSubmatch(placeholder)
		if len(matches) < 2 || matches[1] == "" {
			return raw
		}
		index, parseErr := strconv.Atoi(matches[1])
		if parseErr != nil || index < 0 || index >= len(arguments) {
			return ""
		}
		return arguments[index]
	})
	return expanded, nil
}
