package sessiontitle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
)

var fileWrapperRe = regexp.MustCompile(`<<<<< FILE:[^>]+>>>>>`)

type Candidate struct {
	ShortTitle string  `json:"short_title"`
	LongTitle  string  `json:"long_title"`
	Confidence float64 `json:"confidence"`
}

type Options struct {
	// MaxInputTokens caps the approximate prompt input budget for conversation text.
	// A token is approximated as four UTF-8 bytes; this is deliberately conservative
	// and provider-agnostic enough for autotitling.
	MaxInputTokens int
}

func Generate(ctx context.Context, provider llm.Provider, sess *session.Session, messages []session.Message) (Candidate, error) {
	return GenerateWithOptions(ctx, provider, sess, messages, Options{})
}

func GenerateWithOptions(ctx context.Context, provider llm.Provider, sess *session.Session, messages []session.Message, opts Options) (Candidate, error) {
	if provider == nil {
		return Candidate{}, fmt.Errorf("provider is nil")
	}
	if sess == nil {
		return Candidate{}, fmt.Errorf("session is nil")
	}

	slice := BuildConversationSliceWithOptions(messages, opts)
	if strings.TrimSpace(slice) == "" {
		return Candidate{}, fmt.Errorf("no usable conversation text")
	}

	prompt := fmt.Sprintf(`Produce two session titles from this conversation slice.

Rules:
- short_title: 2 to 8 words
- long_title: 5 to 18 words
- be specific, concrete, and friendly
- prefer the main task, decision, or topic
- do not use filler like "Help with", "Discussion about", or "Question about"
- do not mention "session", "chat", or "conversation"
- if the content is too trivial or unclear, return null for both titles and 0 confidence
- return JSON only

JSON schema:
{"short_title":"string|null","long_title":"string|null","confidence":0.0}

Conversation slice:
%s`, slice)

	text, err := completeText(ctx, provider, prompt, 30*time.Second)
	if err != nil {
		return Candidate{}, err
	}
	cand, err := ParseCandidate(text)
	if err != nil {
		return Candidate{}, err
	}
	if !Acceptable(cand) {
		return cand, fmt.Errorf("generated titles rejected: short=%q (%d words) long=%q (%d words) conf=%.2f",
			cand.ShortTitle, wordCount(cand.ShortTitle),
			cand.LongTitle, wordCount(cand.LongTitle),
			cand.Confidence)
	}
	return cand, nil
}

func BuildConversationSlice(messages []session.Message) string {
	return BuildConversationSliceWithOptions(messages, Options{})
}

func BuildConversationSliceWithOptions(messages []session.Message, opts Options) string {
	if len(messages) == 0 {
		return ""
	}
	maxChars := opts.MaxInputTokens * 4
	if maxChars <= 0 {
		maxChars = 2800
	}

	type line struct {
		text  string
		chars int
	}
	var lines []line
	for _, m := range messages {
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
			continue
		}
		text := cleanMessageText(m.TextContent)
		if text == "" {
			continue
		}
		prefix := "Assistant: "
		if m.Role == llm.RoleUser {
			prefix = "User: "
		}
		entry := prefix + text
		lines = append(lines, line{text: entry, chars: len(entry) + 1})
	}
	if len(lines) == 0 {
		return ""
	}

	total := 0
	for _, l := range lines {
		total += l.chars
	}
	if total <= maxChars {
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.text)
		}
		return strings.Join(out, "\n")
	}

	// Keep both the start and the latest turns. The opening usually names the task;
	// the tail is what lets a later refresh improve an already-generated title.
	headBudget := maxChars / 2
	tailBudget := maxChars - headBudget - len("[… earlier middle turns omitted …]\n")
	if tailBudget < maxChars/4 {
		tailBudget = maxChars / 2
		headBudget = maxChars - tailBudget
	}

	selected := make([]string, 0, len(lines)+1)
	used := 0
	headEnd := 0
	for headEnd < len(lines) {
		l := lines[headEnd]
		if used > 0 && used+l.chars > headBudget {
			break
		}
		if l.chars > headBudget && used == 0 {
			selected = append(selected, truncateRunes(l.text, headBudget))
			used = headBudget
			headEnd++
			break
		}
		selected = append(selected, l.text)
		used += l.chars
		headEnd++
	}

	tailStart := len(lines)
	used = 0
	for tailStart > headEnd {
		l := lines[tailStart-1]
		if used > 0 && used+l.chars > tailBudget {
			break
		}
		if l.chars > tailBudget && used == 0 {
			tailStart--
			break
		}
		used += l.chars
		tailStart--
	}
	if tailStart > headEnd {
		selected = append(selected, "[… earlier middle turns omitted …]")
	}
	if tailStart < len(lines) {
		for _, l := range lines[tailStart:] {
			if l.chars > tailBudget && len(selected) > 0 {
				selected = append(selected, truncateRunes(l.text, tailBudget))
				continue
			}
			selected = append(selected, l.text)
		}
	}
	return strings.Join(selected, "\n")
}

func ParseCandidate(raw string) (Candidate, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx >= 0 {
		cleaned = cleaned[:idx+1]
	}
	var payload struct {
		ShortTitle *string `json:"short_title"`
		LongTitle  *string `json:"long_title"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return Candidate{}, fmt.Errorf("parse title json: %w", err)
	}
	cand := Candidate{Confidence: payload.Confidence}
	if payload.ShortTitle != nil {
		cand.ShortTitle = normalizeTitle(*payload.ShortTitle)
	}
	if payload.LongTitle != nil {
		cand.LongTitle = normalizeTitle(*payload.LongTitle)
	}
	return cand, nil
}

func Acceptable(c Candidate) bool {
	if strings.TrimSpace(c.ShortTitle) == "" || strings.TrimSpace(c.LongTitle) == "" {
		return false
	}
	// confidence=0 means the model omitted the field; treat as acceptable.
	if c.Confidence > 0 && c.Confidence < 0.45 {
		return false
	}
	if n := wordCount(c.ShortTitle); n < 2 || n > 8 {
		return false
	}
	if n := wordCount(c.LongTitle); n < 5 || n > 18 {
		return false
	}
	if isGenericTitle(c.ShortTitle) || isGenericTitle(c.LongTitle) {
		return false
	}
	return true
}

func completeText(ctx context.Context, provider llm.Provider, prompt string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stream, err := provider.Stream(ctx, llm.Request{
		Messages: []llm.Message{
			llm.SystemText("You generate concise, specific titles and reply exactly as requested."),
			llm.UserText(prompt),
		},
		MaxTurns: 1,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var b strings.Builder
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch ev.Type {
		case llm.EventTextDelta:
			b.WriteString(ev.Text)
		case llm.EventError:
			if ev.Err != nil {
				return "", ev.Err
			}
			return "", fmt.Errorf("provider returned error event")
		}
	}
	return b.String(), nil
}

func cleanMessageText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = fileWrapperRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func truncateWords(s string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) <= maxWords {
		return s
	}
	return strings.Join(fields[:maxWords], " ")
}

func truncateRunes(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	if len(s) <= maxChars {
		return s
	}
	out := make([]rune, 0, maxChars)
	used := 0
	for _, r := range s {
		w := len(string(r))
		if used+w > maxChars {
			break
		}
		out = append(out, r)
		used += w
	}
	return strings.TrimSpace(string(out)) + " …"
}

func normalizeTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) && r != '&' && r != '/'
	})
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func wordCount(s string) int {
	return len(strings.Fields(strings.TrimSpace(s)))
}

func isGenericTitle(s string) bool {
	norm := strings.ToLower(strings.TrimSpace(s))
	generic := []string{
		"general discussion",
		"brainstorming ideas",
		"technical help",
		"follow-up question",
		"help with",
		"discussion about",
		"question about",
		"fix web issue",
		"making it better",
		"fixing this issue",
	}
	for _, g := range generic {
		if norm == g || strings.HasPrefix(norm, g+" ") {
			return true
		}
	}
	return false
}
