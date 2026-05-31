package llm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// Classify sends a short prompt and returns the first lowercase word in the model response.
func Classify(ctx context.Context, provider Provider, prompt string, timeout time.Duration) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("provider is nil")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	classifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := provider.Stream(classifyCtx, Request{
		Messages: []Message{UserText(prompt)},
		MaxTurns: 1,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var buf strings.Builder
	for {
		ev, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return "", recvErr
		}
		if ev.Type == EventTextDelta {
			buf.WriteString(ev.Text)
		}
	}

	resp := strings.ToLower(strings.TrimSpace(buf.String()))
	if resp == "" {
		return "", fmt.Errorf("empty classification response")
	}
	fields := strings.Fields(resp)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty classification response")
	}
	return fields[0], nil
}

type InterruptAction int

const (
	InterruptCancel InterruptAction = iota
	InterruptInterject
)

// InterruptActivity summarizes active stream state for interrupt classification.
type InterruptActivity struct {
	CurrentTask string
	ToolsRun    []string
	ActiveTool  string
	ProseLen    int
}

// ClassifyInterruptImmediate applies zero-latency local heuristics for common
// interrupt intents. It returns (action, true) when a heuristic matched.
func ClassifyInterruptImmediate(msg string) (InterruptAction, bool) {
	return classifyInterruptHeuristic(msg)
}

func classifyInterruptHeuristic(msg string) (InterruptAction, bool) {
	normalized := strings.ToLower(strings.TrimSpace(msg))
	if normalized == "" {
		return InterruptInterject, true
	}

	overrides := []string{"/stop", "/cancel", "/takeover", "stop", "cancel", "abort", "never mind", "nevermind", "forget it"}
	for _, w := range overrides {
		if normalized == w || strings.HasPrefix(normalized, w+" ") {
			return InterruptCancel, true
		}
	}

	return InterruptInterject, false
}

func classifyInterruptFallback(msg string, activity InterruptActivity) InterruptAction {
	normalized := strings.ToLower(strings.TrimSpace(msg))
	if normalized == "" {
		return InterruptInterject
	}

	// If the user is obviously amending the current work, keep the current run
	// alive and inject the note at the next legal boundary.
	steeringPrefixes := []string{
		"also", "and ", "plus", "include", "add ", "use ", "make sure", "don't", "dont",
		"instead", "not ", "actually,", "correction", "wait", "one more", "as well", "what about",
	}
	for _, prefix := range steeringPrefixes {
		if normalized == strings.TrimSpace(prefix) || strings.HasPrefix(normalized, prefix) {
			return InterruptInterject
		}
	}

	active := strings.TrimSpace(activity.ActiveTool) != "" || strings.TrimSpace(activity.CurrentTask) != "" || len(activity.ToolsRun) > 0 || activity.ProseLen > 0
	if !active {
		return InterruptInterject
	}

	// Short standalone prompts sent while the agent is busy are usually the user
	// replacing the task, not carefully steering the old one. This is especially
	// important in the web UI where the send button becomes "Interject" during a
	// stream; defaulting every ambiguous message to interject makes sessions go
	// off chasing stale work.
	standalonePrefixes := []string{
		"who", "what", "when", "where", "why", "how", "can ", "could ", "please ",
		"make ", "find ", "show ", "tell ", "say ", "list ", "compare ", "write ", "create ",
	}
	for _, prefix := range standalonePrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return InterruptCancel
		}
	}
	if strings.Contains(normalized, " vs ") || strings.Contains(normalized, " versus ") || strings.HasSuffix(normalized, "?") {
		return InterruptCancel
	}

	return InterruptInterject
}

// ClassifyInterrupt decides how to handle a new user message while a stream is active.
// It uses instant heuristics first and then an optional fast LLM call.
func ClassifyInterrupt(ctx context.Context, fastProvider Provider, msg string, activity InterruptActivity) InterruptAction {
	if action, ok := classifyInterruptHeuristic(msg); ok {
		return action
	}
	if fastProvider == nil {
		return classifyInterruptFallback(msg, activity)
	}

	toolsRun := "none"
	if len(activity.ToolsRun) > 0 {
		toolsRun = strings.Join(activity.ToolsRun, ", ")
	}
	agentActivity := "thinking"
	if strings.TrimSpace(activity.ActiveTool) != "" {
		agentActivity = "running tool " + strings.TrimSpace(activity.ActiveTool)
	} else if strings.TrimSpace(activity.CurrentTask) != "" {
		agentActivity = strings.TrimSpace(activity.CurrentTask)
	}

	prompt := fmt.Sprintf(`You are classifying a user message sent while an AI agent is busy working.

Agent is currently: %s
Agent task: %s
Tools already executed: %s
Agent has produced: %d characters so far.

User's new message: %q

Classify as one word:
- cancel: user wants to stop current work and replace it with this new message
- interject: user wants to steer or correct the current work; agent should continue and incorporate it at the next legal boundary

Reply with exactly one word. If unsure, reply interject.`,
		agentActivity,
		strings.TrimSpace(activity.CurrentTask),
		toolsRun,
		activity.ProseLen,
		strings.TrimSpace(msg),
	)

	decision, err := Classify(ctx, fastProvider, prompt, 3*time.Second)
	if err != nil {
		return classifyInterruptFallback(msg, activity)
	}

	switch strings.TrimSpace(decision) {
	case "cancel", "abort", "stop":
		return InterruptCancel
	case "interject", "inject", "queue", "wait", "later":
		return InterruptInterject
	default:
		return InterruptInterject
	}
}
