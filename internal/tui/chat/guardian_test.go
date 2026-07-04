package chat

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/tools"
	"github.com/samsaffron/term-llm/internal/ui"
)

func TestGuardianReviewMsgAppearsInStreamAndFooter(t *testing.T) {
	m := newTestChatModel(true)
	m.width = 80
	message := "guardian: approved (low risk, user-requested)"

	result, _ := m.Update(GuardianReviewMsg{Message: message})
	rm := result.(*Model)
	if !strings.Contains(rm.footerMessage, message) {
		t.Fatalf("footerMessage = %q, want %q", rm.footerMessage, message)
	}
	plain := ui.StripANSI(rm.tracker.RenderUnflushed(80, ui.RenderMarkdown, false))
	if !strings.Contains(plain, message) {
		t.Fatalf("stream output = %q, want guardian message", plain)
	}
}

func TestGuardianFooterToneDenialBeatsApprovedInRationale(t *testing.T) {
	if got := guardianFooterTone("guardian: denied: user never approved this command"); got != "warning" {
		t.Fatalf("tone = %q, want warning", got)
	}
}

func TestGuardianReviewRepeatedAboveApprovalPrompt(t *testing.T) {
	m := newTestChatModel(true)
	m.width = 80
	m.altScreen = true
	message := "guardian: denied: user never approved this command"

	result, _ := m.Update(GuardianReviewMsg{Message: message})
	m = result.(*Model)
	result, _ = m.Update(ApprovalRequestMsg{
		Path:    "rm -rf important",
		IsShell: true,
		DoneCh:  make(chan tools.ApprovalResult, 1),
	})
	m = result.(*Model)

	plain := ui.StripANSI(m.tracker.RenderUnflushed(80, ui.RenderMarkdown, false))
	if !strings.Contains(plain, "Guardian review before approval:") {
		t.Fatalf("scrollback missing approval-context header: %q", plain)
	}
	if strings.Count(plain, "user never approved this command") < 2 {
		t.Fatalf("guardian rationale should appear once as event and once above approval prompt: %q", plain)
	}
	if m.lastGuardianReviewForApproval != "" {
		t.Fatalf("lastGuardianReviewForApproval was not cleared: %q", m.lastGuardianReviewForApproval)
	}
}
