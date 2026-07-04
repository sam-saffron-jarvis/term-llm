package cmd

import (
	"strings"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/tools"
	"github.com/spf13/cobra"
)

func approvalModeFromSession(value session.SessionApprovalMode) tools.ApprovalMode {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case string(session.ApprovalModeAuto):
		return tools.ModeAuto
	case string(session.ApprovalModeYolo):
		// Stored yolo is intentionally not restored on cold resume. It is only
		// honored when explicitly requested for this process via --yolo.
		return tools.ModePrompt
	default:
		return tools.ModePrompt
	}
}

func approvalModeToSession(value tools.ApprovalMode) session.SessionApprovalMode {
	switch value {
	case tools.ModeAuto:
		return session.ApprovalModeAuto
	case tools.ModeYolo:
		return session.ApprovalModeYolo
	default:
		return session.ApprovalModePrompt
	}
}

func approvalModeFromConfig(cfg *config.Config) tools.ApprovalMode {
	if cfg == nil {
		return tools.ModePrompt
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Approval.DefaultMode)) {
	case "auto":
		return tools.ModeAuto
	default:
		return tools.ModePrompt
	}
}

func resolveChatApprovalMode(cmd *cobra.Command, cfg *config.Config, sess *session.Session) tools.ApprovalMode {
	if cmd != nil {
		if cmd.Flags().Changed("yolo") && chatYolo {
			return tools.ModeYolo
		}
		if cmd.Flags().Changed("auto") && chatAutoApproval {
			return tools.ModeAuto
		}
	}
	if chatYolo {
		return tools.ModeYolo
	}
	if chatAutoApproval {
		return tools.ModeAuto
	}
	if sess != nil && strings.TrimSpace(string(sess.ApprovalMode)) != "" {
		return approvalModeFromSession(sess.ApprovalMode)
	}
	return approvalModeFromConfig(cfg)
}
