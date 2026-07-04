package cmd

import (
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/tools"
)

func TestApprovalModeFromSessionDoesNotRestoreYolo(t *testing.T) {
	if got := approvalModeFromSession(session.ApprovalModeAuto); got != tools.ModeAuto {
		t.Fatalf("auto restored as %v, want auto", got)
	}
	if got := approvalModeFromSession(session.ApprovalModePrompt); got != tools.ModePrompt {
		t.Fatalf("prompt restored as %v, want prompt", got)
	}
	if got := approvalModeFromSession(session.ApprovalModeYolo); got != tools.ModePrompt {
		t.Fatalf("stored yolo restored as %v, want prompt downgrade", got)
	}
}

func TestApprovalModeFromConfigAllowsAutoDefault(t *testing.T) {
	cfg := &config.Config{Approval: config.ApprovalConfig{DefaultMode: "auto"}}
	if got := approvalModeFromConfig(cfg); got != tools.ModeAuto {
		t.Fatalf("config default mode = %v, want auto", got)
	}
}

func TestResolveChatApprovalModeUsesConfigForNewSession(t *testing.T) {
	oldYolo, oldAuto := chatYolo, chatAutoApproval
	defer func() { chatYolo, chatAutoApproval = oldYolo, oldAuto }()
	chatYolo, chatAutoApproval = false, false
	cfg := &config.Config{Approval: config.ApprovalConfig{DefaultMode: "auto"}}
	if got := resolveChatApprovalMode(nil, cfg, nil); got != tools.ModeAuto {
		t.Fatalf("resolved mode = %v, want auto", got)
	}
}

func TestResolveChatApprovalModeSessionOverridesConfig(t *testing.T) {
	oldYolo, oldAuto := chatYolo, chatAutoApproval
	defer func() { chatYolo, chatAutoApproval = oldYolo, oldAuto }()
	chatYolo, chatAutoApproval = false, false
	cfg := &config.Config{Approval: config.ApprovalConfig{DefaultMode: "auto"}}
	sess := &session.Session{ApprovalMode: session.ApprovalModePrompt}
	if got := resolveChatApprovalMode(nil, cfg, sess); got != tools.ModePrompt {
		t.Fatalf("resolved mode = %v, want session prompt", got)
	}
}
