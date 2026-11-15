package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	imageapp "github.com/term-llm/term-llm/internal/app/image"
	"github.com/term-llm/term-llm/internal/image"
	"github.com/term-llm/term-llm/internal/input"
	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/signal"
	"github.com/term-llm/term-llm/internal/terminalpolicy"
	"github.com/term-llm/term-llm/internal/ui"
	"golang.org/x/term"
)

var (
	imageOperation, imageEditModel, imageNegative, imageEnhancePrompt string
	imageWidth, imageHeight, imageSteps, imageScale                   int
	imageCFG, imageCreativity, imageReplication                       float64
	imageSafeMode, imageEnhance                                       bool
	imageTimeout                                                      time.Duration
	imageInputs                                                       []string
	imageProvider                                                     string
	imageOutput                                                       string
	imageSize                                                         string
	imageAspectRatio                                                  string
	imageQuality                                                      string
	imageBackground                                                   string
	imageNoDisplay                                                    bool
	imageNoClipboard                                                  bool
	imageNoSave                                                       bool
	imageNoSpinner                                                    bool
	imageDebug                                                        bool
)

var (
	imageIsTerminal = term.IsTerminal
	openImageTTY    = os.OpenFile
	imageLoadConfig = loadConfigWithSetup
)

var imageCmd = &cobra.Command{
	Use:   "image <prompt>",
	Short: "Generate, edit or process images using AI",
	Long: `Generate images from text prompts or edit existing images.

By default:
  - Saves to ~/Pictures/term-llm/
  - Displays via icat (if available)
  - Copies to clipboard

Multi-image editing (Gemini, OpenRouter, Venice):
  Use -i multiple times to combine or work with multiple images.

Examples:
  term-llm image "a robot cat on a rainbow"
  term-llm image "make it purple" -i photo.png
  term-llm image "add a hat" -i clipboard           # edit from clipboard
  term-llm image "combine these" -i a.png -i b.png  # multi-image (Gemini/OpenRouter/Venice)
  term-llm image "sunset over mountains" --provider flux
  term-llm image "a pelican riding a bike" --provider local-images
  term-llm image "logo design" -o ./output.png --no-display
  term-llm image "robot cat" -o - | term-llm video "animate it" -i -
  echo "a sunset" | term-llm image                  # prompt from stdin
  term-llm image -p venice -i photo.png --operation upscale --scale 2
  term-llm image -p venice -i photo.png --operation background-remove

Venice generation: --width/--height, --steps, --cfg-scale, --negative-prompt, --safe-mode.
Editing: --edit-model overrides configured editor; Venice accepts HTTP(S) inputs.
Local processing: --operation, with upscale-only --scale/--enhance/--enhance-prompt/
--enhance-creativity/--replication. Processing accepts one local file, no prompt.
--timeout is provider-neutral; cancellation does not roll back remote work.`,
	Args: cobra.ArbitraryArgs,
	RunE: runImage,
}

func init() {
	imageCmd.Flags().StringArrayVarP(&imageInputs, "input", "i", nil, "Input path/clipboard, or Venice HTTP(S) URL (repeatable)")
	imageCmd.Flags().StringVarP(&imageProvider, "provider", "p", "", "Override image provider (built-in or configured openai_compatible name)")
	imageCmd.Flags().StringVarP(&imageOutput, "output", "o", "", "Custom output path")
	imageCmd.Flags().StringVarP(&imageSize, "size", "s", "", "Image resolution: 1K, 2K, 4K; native omits resolution (Venice only)")
	imageCmd.Flags().StringVar(&imageAspectRatio, "aspect-ratio", "", "Image aspect ratio (for example 1:1, 16:9, or 9:16)")
	imageCmd.Flags().StringVarP(&imageQuality, "quality", "q", "", "Image quality (auto, low, medium, high, xhigh, or max)")
	imageCmd.Flags().StringVar(&imageBackground, "background", "", "Image background (auto, opaque, or transparent)")
	imageCmd.Flags().BoolVar(&imageNoDisplay, "no-display", false, "Skip terminal display")
	imageCmd.Flags().BoolVar(&imageNoClipboard, "no-clipboard", false, "Skip clipboard copy")
	imageCmd.Flags().BoolVar(&imageNoSave, "no-save", false, "Don't save to default location (use with -o)")
	imageCmd.Flags().BoolVar(&imageNoSpinner, "no-spinner", false, "Disable interactive progress display")
	imageCmd.Flags().BoolVarP(&imageDebug, "debug", "d", false, "Show debug information")

	imageCmd.RegisterFlagCompletionFunc("provider", ImageProviderFlagCompletion)

	imageCmd.Flags().StringVar(&imageOperation, "operation", "", "Local processing: upscale or background-remove (no prompt)")
	imageCmd.Flags().StringVar(&imageEditModel, "edit-model", "", "Per-call Venice editor (overrides configured edit_model)")
	imageCmd.Flags().IntVar(&imageWidth, "width", 0, "Venice generation width; requires --height, conflicts with --size/--aspect-ratio")
	imageCmd.Flags().IntVar(&imageHeight, "height", 0, "Venice generation height; requires --width")
	imageCmd.Flags().IntVar(&imageSteps, "steps", 0, "Venice generation steps (positive)")
	imageCmd.Flags().Float64Var(&imageCFG, "cfg-scale", 0, "Venice generation CFG scale (finite)")
	imageCmd.Flags().StringVar(&imageNegative, "negative-prompt", "", "Venice generation negative prompt")
	imageCmd.Flags().BoolVar(&imageSafeMode, "safe-mode", false, "Enable Venice generation safe mode")
	imageCmd.Flags().DurationVar(&imageTimeout, "timeout", mediautil.DefaultMediaTimeout, "Paid request budget (default 1h); shorter deadlines risk losing paid output")
	imageCmd.Flags().IntVar(&imageScale, "scale", 0, "Upscale factor (positive)")
	imageCmd.Flags().BoolVar(&imageEnhance, "enhance", false, "Enable upscale enhancement")
	imageCmd.Flags().StringVar(&imageEnhancePrompt, "enhance-prompt", "", "Upscale enhancement prompt")
	imageCmd.Flags().Float64Var(&imageCreativity, "enhance-creativity", 0, "Upscale enhancement creativity (finite)")
	imageCmd.Flags().Float64Var(&imageReplication, "replication", 0, "Upscale replication (finite)")
	imageCmd.RegisterFlagCompletionFunc("size", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"1K", "2K", "4K", "native"}, cobra.ShellCompDirectiveNoFileComp
	})
	imageCmd.RegisterFlagCompletionFunc("operation", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"upscale", "background-remove"}, cobra.ShellCompDirectiveNoFileComp
	})
	rootCmd.AddCommand(imageCmd)
}

func runImage(cmd *cobra.Command, args []string) error {
	options := captureImageOptions(cmd)
	if err := options.Validate(len(args) > 0); err != nil {
		return err
	}
	var prompt string
	if len(args) > 0 {
		prompt = strings.Join(args, " ")
	} else if imageOperation == "" {
		stdinContent, err := input.ReadStdin()
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		prompt = strings.TrimSpace(stdinContent)
	}

	if prompt == "" && imageOperation == "" {
		return fmt.Errorf("prompt required: provide as argument or via stdin")
	}

	ctx, stop := signal.NotifyContextWithParent(cmd.Context())
	defer stop()

	// Load config (requires setup wizard if no config exists)
	cfg, err := imageLoadConfig()
	if err != nil {
		return err
	}

	// Initialize theme from config
	initThemeFromConfig(cfg)

	// Validate --size flag
	if err := imageapp.ValidateSize(imageSize); err != nil {
		return err
	}
	if err := image.ValidateAspectRatio(imageAspectRatio); err != nil {
		return err
	}
	if err := image.ValidateQuality(imageQuality); err != nil {
		return err
	}
	if err := image.ValidateBackground(imageBackground); err != nil {
		return err
	}

	// Create image provider
	provider, err := image.NewImageProvider(cfg, imageProvider)
	if err != nil {
		return err
	}

	if imageDebug {
		fmt.Fprintf(os.Stderr, "Using provider: %s\n", provider.Name())
		fmt.Fprintf(os.Stderr, "Prompt: %q\n", prompt)
	}

	label, generate, err := options.Prepare(provider, prompt, func(path string) ([]byte, error) {
		if path == "clipboard" {
			return image.ReadFromClipboard()
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read input image: %w", err)
		}
		return data, nil
	})
	if err != nil {
		return err
	}
	// Setup, credentials, approvals and local preparation do not consume the paid request budget.
	ctx, cancel := options.RequestContext(ctx)
	defer cancel()
	result, err := runImageWithSpinner(ctx, provider, generate, label)
	if err != nil {
		return fmt.Errorf("image request failed: %w", image.RequestError(err))
	}
	if imageOperation != "" {
		prompt = options.OutputName()
	}

	// Determine output path
	var outputPath string
	if imageOutput == "-" {
		if _, err := cmd.OutOrStdout().Write(result.Data); err != nil {
			return fmt.Errorf("failed to write image to stdout: %w", err)
		}
		return nil
	} else if imageOutput != "" {
		// Custom output path specified
		outputPath = imageOutput
		if err := os.WriteFile(outputPath, result.Data, 0644); err != nil {
			return fmt.Errorf("failed to write image: %w", err)
		}
	} else if !imageNoSave {
		// Save to default location
		outputPath, err = image.SaveImage(result.Data, cfg.Image.OutputDir, prompt)
		if err != nil {
			return fmt.Errorf("failed to save image: %w", err)
		}
	}

	// Display and clipboard integration are interactive terminal features.
	hasTTY := hasInteractiveImageTTY()

	if outputPath != "" {
		fmt.Fprintf(os.Stderr, "Saved to: %s\n", outputPath)
		recordImageDirect(cfg, prompt, outputPath, result, provider.Name())
	}

	// Skip display and clipboard when no TTY (e.g. piped/scripted usage)
	if !hasTTY {
		return nil
	}

	// Display via icat
	if !imageNoDisplay && outputPath != "" {
		if err := image.DisplayImage(outputPath); err != nil {
			if imageDebug {
				fmt.Fprintf(os.Stderr, "Display warning: %v\n", err)
			}
		}
	}

	// Copy to clipboard
	if !imageNoClipboard && outputPath != "" {
		if err := image.CopyToClipboard(outputPath, result.Data); err != nil {
			if imageDebug {
				fmt.Fprintf(os.Stderr, "Clipboard warning: %v\n", err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Copied to clipboard")
		}
	}

	return nil
}

// imageSpinnerModel is a simple spinner for image generation
type imageSpinnerModel struct {
	spinner  spinner.Model
	message  string
	result   *image.ImageResult
	err      error
	done     bool
	start    time.Time
	provider image.ImageProvider
	generate func(context.Context) (*image.ImageResult, error)
	ctx      context.Context
	cancel   context.CancelFunc
	styles   *ui.Styles
}

type imageResultMsg struct {
	result *image.ImageResult
	err    error
}

func (m imageSpinnerModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			result, err := m.generate(m.ctx)
			return imageResultMsg{result: result, err: err}
		},
	)
}

func (m imageSpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.cancel()
			return m, tea.Quit
		}

	case imageResultMsg:
		m.result = msg.result
		m.err = msg.err
		m.done = true
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m imageSpinnerModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	s := ui.StreamingIndicator{
		Spinner:    m.spinner.View(),
		Phase:      m.message,
		Elapsed:    time.Since(m.start),
		ShowCancel: true,
	}.Render(m.styles) + "\n"
	return tea.NewView(s)
}

func imageSpinnerEnvironment(environ []string) []string {
	filtered := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch name {
		case "TERM", "TERM_PROGRAM", "WT_SESSION":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	// Bubble Tea v2 probes Kitty/Ghostty/etc. for synchronized output (modes
	// 2026/2027) as soon as the renderer starts. The image command's spinner is
	// short-lived, so fast providers can exit before those terminal replies are
	// consumed; the user's shell then echoes stray "^[?2026;...$y" bytes. Force a
	// conservative environment for this tiny spinner only. The actual image
	// renderer still uses the real process environment for Kitty/iTerm/Sixel.
	filtered = append(filtered, "TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal")
	return filtered
}

func hasInteractiveImageTTY() bool {
	if !terminalpolicy.OutputInteractiveWith(os.Stderr, imageIsTerminal, os.Getenv) {
		return false
	}
	tty, err := openImageTTY("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

func shouldUseImageSpinner() bool {
	if imageNoSpinner || terminalpolicy.EnvironmentEnabled(os.Getenv("TERM_LLM_NO_SPINNER")) {
		return false
	}
	return terminalpolicy.InteractiveWith(os.Stdin, os.Stderr, imageIsTerminal, os.Getenv)
}

func runImageWithSpinner(ctx context.Context, provider image.ImageProvider, generate func(context.Context) (*image.ImageResult, error), message string) (*image.ImageResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !shouldUseImageSpinner() {
		return generate(ctx)
	}

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	m := imageSpinnerModel{
		spinner:  s,
		message:  message,
		start:    time.Now(),
		provider: provider,
		generate: generate,
		ctx:      ctx,
		cancel:   cancel,
		styles:   ui.DefaultStyles(),
	}

	// Opening the controlling terminal can still fail after the stream checks.
	tty, err := openImageTTY("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// Fallback: run without spinner UI
		return generate(ctx)
	}
	defer tty.Close()

	p := tea.NewProgram(m,
		tea.WithInput(tty),
		tea.WithOutput(os.Stderr),
		tea.WithEnvironment(imageSpinnerEnvironment(os.Environ())),
		tea.WithoutSignalHandler(),
	)
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	final := finalModel.(imageSpinnerModel)
	if final.err != nil {
		return nil, final.err
	}
	if final.result == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("cancelled")
	}
	return final.result, nil
}
