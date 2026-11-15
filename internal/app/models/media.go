package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/venice"
)

func (o *Options) runMedia(cmd *cobra.Command, cfg *config.Config) error {
	provider := o.Provider
	if provider == "" {
		provider = cfg.DefaultProvider
	}
	if provider != "venice" {
		return fmt.Errorf("--type is supported only with --provider venice")
	}
	if o.Type != "image" && o.Type != "inpaint" && o.Type != "video" {
		return fmt.Errorf("--type must be image, inpaint, or video")
	}
	if o.Refresh {
		return fmt.Errorf("--refresh is ChatGPT-only; typed media listing always refreshes explicitly")
	}
	root, err := cfg.ProviderBaseURL("venice", cfg.Image.Venice.BaseURL, venice.BaseURL)
	if err != nil {
		return err
	}
	key, err := cfg.Image.VeniceKey().Resolve()
	if err != nil {
		return fmt.Errorf("image.venice.api_key: %w", err)
	}
	if key == "" {
		return fmt.Errorf("image.venice.api_key is required for media catalogs")
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	models, err := venice.FetchMediaModels(ctx, &http.Client{}, key, root, o.Type)
	if err != nil {
		return err
	}
	if err := venice.StoreMediaModels(root, o.Type, models); err != nil {
		return fmt.Errorf("save media model cache: %w", err)
	}
	if o.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(models)
	}
	for _, model := range models {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s", model.ID, model.Type, model.DisplayName)
		if model.Media != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "\tprivacy=%s\tkind=%s\tdurations=%v\tresolutions=%v", model.Media.Privacy, model.Media.ModelType, model.Media.Durations, model.Media.Resolutions)
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}
