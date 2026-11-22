package wire

import (
	"errors"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/session"
)

// EditGoal validates a goal-control payload and projects its next durable value.
// Authorization, persistence and active-turn ownership belong to the caller.
func EditGoal(current *session.Goal, action, objective string, tokenBudget *int, now time.Time) (*session.Goal, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "set"
	}
	var goal *session.Goal
	switch action {
	case "set", "edit":
		objective = strings.TrimSpace(objective)
		if objective == "" {
			return nil, errors.New("objective is required")
		}
		budget := 0
		if tokenBudget != nil {
			if *tokenBudget < 0 {
				return nil, errors.New("token_budget must be non-negative")
			}
			budget = *tokenBudget
		}
		if action == "edit" && current != nil && current.Exists() {
			goal = current.Clone()
			goal.Objective = objective
			if tokenBudget != nil {
				goal.TokenBudget = budget
			}
			goal.Status = session.GoalStatusActive
			goal.PausedAt = time.Time{}
			goal.CompletedAt = time.Time{}
			goal.BlockedAt = time.Time{}
			goal.UpdatedNotice = true
			goal.UpdatedAt = now
		} else {
			goal = session.NewGoal(objective, budget, now)
		}
	case "pause":
		if current == nil || !current.Exists() {
			return nil, errors.New("no goal is set")
		}
		goal = current.Clone()
		goal.Status = session.GoalStatusPaused
		goal.PausedAt = now
		goal.UpdatedAt = now
	case "resume":
		if current == nil || !current.Exists() {
			return nil, errors.New("no goal is set")
		}
		if current.Status == session.GoalStatusBudgetLimited && current.BudgetExhausted() {
			return nil, errors.New("token budget is exhausted; edit the goal with a higher token_budget before resuming")
		}
		goal = current.Clone()
		goal.Status = session.GoalStatusActive
		goal.PausedAt = time.Time{}
		goal.CompletedAt = time.Time{}
		goal.BlockedAt = time.Time{}
		goal.UpdatedAt = now
	case "clear":
		goal = nil
	default:
		return nil, errors.New("action must be set, edit, pause, resume, or clear")
	}
	return goal, nil
}
