package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/guardian"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/tools"
	"github.com/samsaffron/term-llm/internal/ui"
)

var newGuardianProviderByName = llm.NewProviderByName

const guardianReviewerPoolSize = 3

func preflightHeadlessApproval(cfg *config.Config, resolved resolvedApprovalMode, providerName, modelName string) error {
	if resolved.Mode != tools.ModeAuto {
		return nil
	}
	mgr := tools.NewApprovalManager(tools.NewToolPermissions())
	return applyResolvedApprovalMode(cfg, mgr, resolved, providerName, modelName, approvalRuntimeOptions{Headless: true})
}

// applyResolvedApprovalMode applies requested policy to the actual runtime
// manager. Interactive auto setup degrades to prompt with one warning; headless
// setup fails before work begins. The resolved value remains unchanged so
// callers can persist the requested policy rather than a temporary fallback.
func applyResolvedApprovalMode(cfg *config.Config, approvalMgr *tools.ApprovalManager, resolved resolvedApprovalMode, providerName, modelName string, opts approvalRuntimeOptions) error {
	if approvalMgr == nil {
		// A runtime with no approval-bearing tools has nothing to initialize or
		// review, so even headless auto can start without a manager.
		return nil
	}
	approvalMgr.SetApprovalMode(tools.ModePrompt)
	if resolved.Mode == tools.ModeYolo {
		approvalMgr.SetApprovalMode(tools.ModeYolo)
		return nil
	}

	needsGuardian := resolved.Mode == tools.ModeAuto || opts.PrepareCallbacks
	guardianAvailable := true
	if needsGuardian {
		if err := installGuardianReviewerCallbacks(cfg, approvalMgr, providerName, modelName, opts.Headless); err != nil {
			guardianAvailable = false
			if resolved.Mode == tools.ModeAuto {
				if opts.Headless {
					return fmt.Errorf("auto approval unavailable: %w", err)
				}
				if opts.WarningWriter != nil {
					fmt.Fprintf(opts.WarningWriter, "warning: guardian auto-approval unavailable; using prompt mode: %v\n", err)
				}
			} else if opts.PrepareCallbacks && opts.WarningWriter != nil {
				fmt.Fprintf(opts.WarningWriter, "warning: guardian auto-approval unavailable; auto toggle disabled: %v\n", err)
			}
		}
	}
	if resolved.Mode == tools.ModeAuto && guardianAvailable {
		approvalMgr.SetApprovalMode(tools.ModeAuto)
	}
	return nil
}

func addGuardianUsage(stats *ui.SessionStats, event tools.GuardianEvent) bool {
	if stats == nil || event.Usage.BillableCountersZero() {
		return false
	}
	u := event.Usage
	stats.AddGuardianUsageForModel(event.Model, u.InputTokens, u.OutputTokens, u.CachedInputTokens, u.CacheWriteTokens)
	return true
}

func installGuardianReviewerCallbacks(cfg *config.Config, approvalMgr *tools.ApprovalManager, providerName string, modelName string, headless bool) error {
	if approvalMgr == nil {
		return nil
	}
	if cfg == nil {
		approvalMgr.PolicyReviewFunc = nil
		if approvalMgr.ApprovalMode() == tools.ModeAuto {
			approvalMgr.SetApprovalMode(tools.ModePrompt)
		}
		return fmt.Errorf("auto approval requires configuration and an LLM provider")
	}
	approvalMgr.SetAutoHeadless(headless)
	model := resolveGuardianModelName(cfg, modelName)
	guardianName := strings.TrimSpace(cfg.Guardian.Provider)
	if guardianName == "" {
		guardianName = strings.TrimSpace(providerName)
	}
	if guardianName == "" {
		guardianName = strings.TrimSpace(cfg.DefaultProvider)
	}
	if guardianName == "" {
		approvalMgr.PolicyReviewFunc = nil
		if approvalMgr.ApprovalMode() == tools.ModeAuto {
			approvalMgr.SetApprovalMode(tools.ModePrompt)
		}
		return fmt.Errorf("auto approval requires an LLM provider")
	}
	reviewProvider, err := newGuardianProviderByName(cfg, guardianName, model)
	if err != nil {
		approvalMgr.PolicyReviewFunc = nil
		if approvalMgr.ApprovalMode() == tools.ModeAuto {
			approvalMgr.SetApprovalMode(tools.ModePrompt)
		}
		return fmt.Errorf("guardian provider: %w", err)
	}
	if reviewProvider == nil {
		approvalMgr.PolicyReviewFunc = nil
		if approvalMgr.ApprovalMode() == tools.ModeAuto {
			approvalMgr.SetApprovalMode(tools.ModePrompt)
		}
		return fmt.Errorf("auto approval requires an LLM provider")
	}
	policy, err := guardian.LoadPolicy(cfg.Guardian.PolicyPath)
	if err != nil {
		approvalMgr.PolicyReviewFunc = nil
		if approvalMgr.ApprovalMode() == tools.ModeAuto {
			approvalMgr.SetApprovalMode(tools.ModePrompt)
		}
		return fmt.Errorf("load guardian policy: %w", err)
	}
	reviewers := make([]*guardian.Reviewer, 0, guardianReviewerPoolSize)
	for i := 0; i < guardianReviewerPoolSize; i++ {
		provider := reviewProvider
		if i > 0 {
			provider, err = newGuardianProviderByName(cfg, guardianName, model)
			if err != nil {
				approvalMgr.PolicyReviewFunc = nil
				if approvalMgr.ApprovalMode() == tools.ModeAuto {
					approvalMgr.SetApprovalMode(tools.ModePrompt)
				}
				return fmt.Errorf("guardian provider: %w", err)
			}
			if provider == nil {
				approvalMgr.PolicyReviewFunc = nil
				if approvalMgr.ApprovalMode() == tools.ModeAuto {
					approvalMgr.SetApprovalMode(tools.ModePrompt)
				}
				return fmt.Errorf("auto approval requires an LLM provider")
			}
		}
		reviewer := &guardian.Reviewer{Provider: provider, Model: model, Policy: policy}
		if cfg.Guardian.TimeoutSeconds > 0 {
			reviewer.Timeout = time.Duration(cfg.Guardian.TimeoutSeconds) * time.Second
		}
		reviewers = append(reviewers, reviewer)
	}
	reviewerPool := guardian.NewReviewerPool(reviewers...)
	approvalMgr.PolicyReviewFunc = func(ctx context.Context, req tools.PolicyReviewRequest) (tools.PolicyDecision, error) {
		transcript := make([]guardian.TranscriptEntry, 0, len(req.Transcript))
		for _, e := range req.Transcript {
			transcript = append(transcript, guardian.TranscriptEntry{Role: e.Role, Text: e.Text})
		}
		decision, err := reviewerPool.Review(ctx, guardian.Request{Command: req.Command, WorkDir: req.WorkDir, Transcript: transcript, ApprovalContext: req.ApprovalContext, ScopeID: req.ScopeID})
		result := tools.PolicyDecision{Allowed: decision.Allowed(), RiskLevel: decision.RiskLevel, UserAuthorization: decision.UserAuthorization, Rationale: decision.Rationale, Model: decision.Model, Usage: decision.Usage}
		if err != nil {
			return result, err
		}
		return result, nil
	}
	if approvalMgr.GuardianEventFunc == nil {
		approvalMgr.GuardianEventFunc = func(event tools.GuardianEvent) {
			fmt.Fprintln(os.Stderr, event.Message)
		}
	}
	return nil
}

func resolveGuardianModelName(cfg *config.Config, fallback string) string {
	if cfg == nil {
		return strings.TrimSpace(fallback)
	}
	if model := strings.TrimSpace(cfg.Guardian.Model); model != "" {
		return model
	}
	if guardianName := strings.TrimSpace(cfg.Guardian.Provider); guardianName != "" {
		if pc := cfg.GetProviderConfig(guardianName); pc != nil {
			if model := strings.TrimSpace(pc.Model); model != "" {
				return model
			}
			if model := strings.TrimSpace(pc.FastModel); model != "" {
				return model
			}
		}
		providerType := string(config.InferProviderType(guardianName, ""))
		if model := strings.TrimSpace(llm.ProviderFastModels[providerType]); model != "" {
			return model
		}
	}
	return strings.TrimSpace(fallback)
}
