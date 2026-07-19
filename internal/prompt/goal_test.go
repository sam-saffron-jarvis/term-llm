package prompt

import (
	"strings"
	"testing"
)

func TestBuildGoalPromptPlanGuidanceIsConditional(t *testing.T) {
	goal := GoalPromptData{Objective: "ship it", TokenBudget: 100, TokensUsed: 10}
	baseline := BuildGoalPrompt(goal, GoalPromptContinuation)
	if got := BuildGoalPromptWithPlan(goal, GoalPromptContinuation, false); got != baseline {
		t.Fatalf("disabled plan prompt changed baseline:\n--- baseline\n%s\n--- got\n%s", baseline, got)
	}
	enabled := BuildGoalPromptWithPlan(goal, GoalPromptContinuation, true)
	if !strings.Contains(enabled, "use `update_plan`") || !strings.HasPrefix(enabled, baseline) {
		t.Fatalf("enabled prompt missing conditional guidance:\n%s", enabled)
	}
}

func TestBuildGoalPromptEscapesObjectiveAndBudget(t *testing.T) {
	goal := GoalPromptData{Objective: `finish <danger> & "ship"`, TokenBudget: 100, TokensUsed: 40}
	prompt := BuildGoalPrompt(goal, GoalPromptContinuation)
	if !strings.Contains(prompt, "finish &lt;danger&gt; &amp; &#34;ship&#34;") {
		t.Fatalf("objective was not escaped in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Tokens remaining: 60") {
		t.Fatalf("remaining budget missing from prompt:\n%s", prompt)
	}
}
