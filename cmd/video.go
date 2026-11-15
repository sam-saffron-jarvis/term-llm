package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	videoapp "github.com/term-llm/term-llm/internal/app/video"
	"github.com/term-llm/term-llm/internal/input"
	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/signal"
	"github.com/term-llm/term-llm/internal/video"
)

var (
	videoQueueID         string
	videoSkipQuote       bool
	videoSeedanceConsent bool
	videoInput           string
	videoReferences      []string
	videoProvider        string
	videoOutput          string
	videoModel           string
	videoDuration        string
	videoAspectRatio     string
	videoResolution      string
	videoNegative        string
	videoAudio           bool
	videoDeleteRemote    bool
	videoQuoteOnly       bool
	videoNoWait          bool
	videoJSON            bool
	videoPollInterval    time.Duration
	videoTimeout         time.Duration
	videoDebug           bool
)

var videoCmd = &cobra.Command{
	Use:   "video [prompt]",
	Short: "Generate videos with Venice AI",
	Long: `Generate videos using Venice AI's native video API.

By default:
  - Saves to ~/Pictures/term-llm/
  - Uses text-to-video when no input image is provided
  - Uses image-to-video when --input is provided
  - Quotes the job before queueing it

Examples:
  term-llm video "a corgi surfing at sunset"
  term-llm video "make Romeo blink and wag his tail" -i romeo.png
  term-llm video "cute dog, influencer reacts" -i romeo.png -r pose1.png -r pose2.png
  term-llm video "cyberpunk city, slow dolly shot" --model kling-o3-pro-text-to-video
  term-llm image "robot cat" -o - | term-llm video "animate it" -i -
  term-llm image "character portrait" -o - | term-llm video "make @Image1 wave" --model happyhorse-1-0-reference-to-video -r - --aspect-ratio 16:9
  term-llm video "cute dog, influencer reacts" -i romeo.png --aspect-ratio 9:16 --duration 10s
  term-llm video "astronaut on mars" --quote-only --json
  term-llm video "city lights" --skip-quote --no-wait --json
  term-llm video --model MODEL --queue-id ID --json

Absent resolution/negative prompt use provider defaults, which can change pricing.
Remote media is kept unless --delete-remote is requested after a durable save.
Never retry an uncertain queue; use the printed job identifiers to recover.`,
	Args: cobra.ArbitraryArgs,
	RunE: runVideo,
}

func init() {
	videoCmd.Flags().StringVarP(&videoInput, "input", "i", "", "Input image path, HTTP(S) URL, or - for stdin")
	videoCmd.Flags().StringArrayVarP(&videoReferences, "reference", "r", nil, "Reference image path/HTTP(S) URL/- for stdin (repeatable)")
	videoCmd.Flags().StringVarP(&videoProvider, "provider", "p", "venice", "Video provider override (currently only venice)")
	videoCmd.Flags().StringVarP(&videoOutput, "output", "o", "", "Custom output path")
	videoCmd.Flags().StringVar(&videoModel, "model", "", "Venice video model to use")
	videoCmd.Flags().StringVar(&videoDuration, "duration", video.DefaultDuration, "Video duration in seconds, e.g. 5s")
	videoCmd.Flags().StringVar(&videoAspectRatio, "aspect-ratio", "", "Aspect ratio, e.g. 16:9 or 9:16")
	videoCmd.Flags().StringVar(&videoResolution, "resolution", "", "Video resolution (e.g. 360p, 720p, 4k; absent uses provider default)")
	videoCmd.Flags().StringVar(&videoNegative, "negative-prompt", "", "Negative prompt (absent uses provider default)")
	videoCmd.Flags().BoolVar(&videoAudio, "audio", false, "Request audio when the model supports it")
	videoCmd.Flags().BoolVar(&videoDeleteRemote, "delete-remote", false, "Delete remote media separately, only after durable local save")
	videoCmd.Flags().BoolVar(&videoQuoteOnly, "quote-only", false, "Quote the job and exit without queueing")
	videoCmd.Flags().BoolVar(&videoNoWait, "no-wait", false, "Queue and exit, or retrieve existing status once")
	videoCmd.Flags().BoolVar(&videoJSON, "json", false, "Emit machine-readable JSON to stdout")
	videoCmd.Flags().DurationVar(&videoPollInterval, "poll-interval", video.DefaultPollInterval, "Polling interval while waiting for completion")
	videoCmd.Flags().DurationVar(&videoTimeout, "timeout", video.DefaultTimeout, "Overall paid-job wait budget (default 1h); local timeout does not cancel upstream")
	videoCmd.Flags().BoolVarP(&videoDebug, "debug", "d", false, "Show debug information")

	videoCmd.Flags().StringVar(&videoQueueID, "queue-id", "", "Retrieve an existing job; requires --model, no prompt or generation options")
	videoCmd.Flags().BoolVar(&videoSkipQuote, "skip-quote", false, "Queue without the usual price quote (never retries a queue)")
	videoCmd.Flags().BoolVar(&videoSeedanceConsent, "seedance-consent", false, "Attest to Seedance terms/privacy, legal rights, and screening acknowledgement for this queue")
	rootCmd.AddCommand(videoCmd)
}

func runVideo(cmd *cobra.Command, args []string) error {
	changed := []string{}
	for _, name := range []string{"input", "reference", "duration", "aspect-ratio", "resolution", "negative-prompt", "audio", "seedance-consent", "skip-quote", "quote-only"} {
		if cmd.Flags().Changed(name) {
			changed = append(changed, name)
		}
	}
	if err := videoapp.ValidateMode(videoQueueID, videoModel, len(args) > 0, changed, videoQuoteOnly, videoSkipQuote, videoNoWait, videoPollInterval, videoTimeout, cmd.Flags().Changed("queue-id")); err != nil {
		return err
	}
	if videoInput == "-" && referencesUseStdin(videoReferences) {
		return fmt.Errorf("stdin can only be used for one media input: use either --input - or one --reference -")
	}
	if (videoInput == "-" || referencesUseStdin(videoReferences)) && len(args) == 0 {
		return fmt.Errorf("prompt required as an argument when stdin is used for media input")
	}
	if videoProvider != "" && videoProvider != "venice" {
		return fmt.Errorf("unsupported video provider %q (currently only venice)", videoProvider)
	}
	if err := videoapp.ValidateInputURLs(append([]string{videoInput}, videoReferences...)); err != nil {
		return err
	}
	prompt := ""
	var err error
	if videoQueueID == "" {
		prompt, err = resolveVideoPrompt(args)
		if err != nil {
			return err
		}
	}
	request := video.Request{Prompt: prompt, Model: video.ResolveModel(videoModel, videoInput != ""), Duration: videoDuration, AspectRatio: videoAspectRatio, ImagePath: videoInput, SeedanceConsent: videoSeedanceConsent, Debug: videoDebug, DebugRaw: debugRaw}
	if cmd.Flags().Changed("resolution") {
		v := videoResolution
		request.Resolution = &v
	}
	if cmd.Flags().Changed("negative-prompt") {
		v := videoNegative
		request.NegativePrompt = &v
	}
	if cmd.Flags().Changed("audio") {
		v := videoAudio
		request.Audio = &v
	}
	if videoQueueID != "" {
		request.Model = videoModel
	}
	ctx, stop := signal.NotifyContextWithParent(cmd.Context())
	defer stop()
	cfg, err := loadConfigWithSetup()
	if err != nil {
		return err
	}
	initThemeFromConfig(cfg)
	apiKey, err := cfg.Image.VeniceKey().Resolve()
	if err != nil {
		return fmt.Errorf("image.venice.api_key: %w", err)
	}
	if apiKey == "" {
		return fmt.Errorf("VENICE_API_KEY not configured; set image.venice.api_key")
	}
	provider, err := video.NewConfiguredVeniceProvider(cfg, apiKey)
	if err != nil {
		return err
	}
	options := videoapp.Options{Request: request, QueueID: videoQueueID, Output: videoOutput, OutputDir: cfg.Image.OutputDir, QuoteOnly: videoQuoteOnly, SkipQuote: videoSkipQuote, NoWait: videoNoWait, DeleteRemote: videoDeleteRemote, PollInterval: videoPollInterval, Timeout: videoTimeout, Stderr: cmd.ErrOrStderr()}
	if !cmd.Flags().Changed("timeout") {
		options.Timeout = 0
	}
	options.ReferencePaths = append([]string(nil), videoReferences...)
	options.LoadInputs = func(r *video.Request) error {
		if videoInput != "" {
			if mediautil.IsRemoteReference(videoInput) {
				if err := mediautil.ValidateRemoteURL(videoInput); err != nil {
					return err
				}
				r.ImageURL = videoInput
			} else {
				data, err := loadVideoInput(cmd, videoInput)
				if err != nil {
					return err
				}
				r.ImageData = data
			}
		}
		refs, err := loadVideoReferences(cmd, videoReferences)
		if err != nil {
			return err
		}
		r.ReferenceImages = refs
		return nil
	}
	var result videoapp.Result
	if videoQueueID != "" {
		result, err = videoapp.RetrieveExisting(ctx, provider, options)
	} else {
		result, err = videoapp.Generate(ctx, provider, options)
	}
	if result.Output != nil && !videoJSON {
		fmt.Fprintf(cmd.ErrOrStderr(), "Saved to: %s\n", result.Output.Path)
	}
	if emitErr := emitVideoJSON(cmd, result); emitErr != nil {
		return emitErr
	}
	return err
}

func resolveVideoPrompt(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	stdinContent, err := input.ReadStdin()
	if err != nil {
		return "", fmt.Errorf("failed to read stdin: %w", err)
	}
	prompt := strings.TrimSpace(stdinContent)
	if prompt == "" {
		return "", fmt.Errorf("prompt required: provide as argument or via stdin")
	}
	return prompt, nil
}

func loadVideoInput(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read input image from stdin: %w", err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("read input image from stdin: no data")
		}
		return data, nil
	}
	return video.LoadInputImage(path)
}

func loadVideoReferences(cmd *cobra.Command, paths []string) ([]video.InputImage, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if countStdinReferences(paths) > 1 {
		return nil, fmt.Errorf("only one --reference - is allowed")
	}

	references := make([]video.InputImage, 0, len(paths))
	for _, path := range paths {
		if mediautil.IsRemoteReference(path) {
			if err := mediautil.ValidateRemoteURL(path); err != nil {
				return nil, err
			}
			references = append(references, video.InputImage{URL: path})
			continue
		}
		var data []byte
		var err error
		if path == "-" {
			data, err = io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return nil, fmt.Errorf("read reference image from stdin: %w", err)
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("read reference image from stdin: no data")
			}
		} else {
			data, err = video.LoadInputImage(path)
			if err != nil {
				return nil, err
			}
		}
		references = append(references, video.InputImage{Path: path, Data: data})
	}
	return references, nil
}

func referencesUseStdin(paths []string) bool {
	return countStdinReferences(paths) > 0
}

func countStdinReferences(paths []string) int {
	count := 0
	for _, path := range paths {
		if path == "-" {
			count++
		}
	}
	return count
}

type videoJSONQuote = videoapp.Quote
type videoJSONJob = videoapp.Job
type videoJSONOutput = videoapp.Output
type videoJSONResult = videoapp.Result

func emitVideoJSON(cmd *cobra.Command, result videoJSONResult) error {
	result.AspectRatio = strings.TrimSpace(videoAspectRatio)
	if !videoJSON {
		return nil
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
