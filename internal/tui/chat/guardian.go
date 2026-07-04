package chat

import "strings"

func (m *Model) addGuardianReviewBeforeApprovalPrompt() {
	message := strings.TrimSpace(m.lastGuardianReviewForApproval)
	if message == "" || m.tracker == nil {
		m.lastGuardianReviewForApproval = ""
		return
	}
	m.tracker.AddExternalUIResult("Guardian review before approval:\n" + message)
	m.invalidateViewCache()
	m.lastGuardianReviewForApproval = ""
}

func guardianFooterTone(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.HasPrefix(lower, "guardian: denied"), strings.Contains(lower, "failed"), strings.Contains(lower, "unavailable"), strings.Contains(lower, "circuit breaker"):
		return "warning"
	case strings.HasPrefix(lower, "guardian: approved"):
		return "success"
	default:
		return "muted"
	}
}
