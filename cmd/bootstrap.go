package cmd

import (
	"fmt"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/ui"
)

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, nil
}

func loadConfigWithSetup() (*config.Config, error) {
	if config.NeedsSetup() {
		cfg, err := ui.RunSetupWizard()
		if err != nil {
			return nil, fmt.Errorf("setup cancelled: %w", err)
		}
		return cfg, nil
	}

	return loadConfig()
}

func applyProviderOverrides(cfg *config.Config, provider, model, providerFlag string) error {
	cfg.ApplyOverrides(provider, model)

	if providerFlag == "" {
		return nil
	}

	overrideProvider, overrideModel, err := llm.ParseProviderModel(providerFlag)
	if err != nil {
		return err
	}
	cfg.ApplyOverrides(overrideProvider, overrideModel)
	return nil
}

func initThemeFromConfig(cfg *config.Config) {
	ui.InitTheme(ui.ThemeConfig{
		Primary:   cfg.Theme.Primary,
		Secondary: cfg.Theme.Secondary,
		Success:   cfg.Theme.Success,
		Error:     cfg.Theme.Error,
		Warning:   cfg.Theme.Warning,
		Muted:     cfg.Theme.Muted,
		Text:      cfg.Theme.Text,
		Spinner:   cfg.Theme.Spinner,
	})
}
