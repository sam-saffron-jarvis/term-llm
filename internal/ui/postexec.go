package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/reflow/wordwrap"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/prompt"
	"golang.org/x/term"
)

// ShowCommandHelp renders the full-screen help for a command
func ShowCommandHelp(command, shell string, provider llm.Provider) error {
	ctx := context.Background()

	// Build the help prompt
	helpPrompt := prompt.CommandHelpPrompt(command, shell)

	req := llm.AskRequest{
		Question:     helpPrompt,
		EnableSearch: false,
		Debug:        false,
	}

	// Stream response and render with full-screen markdown
	output := make(chan string)
	errChan := make(chan error, 1)

	go func() {
		errChan <- provider.StreamResponse(ctx, req, output)
	}()

	// Render the streamed content
	err := streamHelpWithGlamour(output)
	if err != nil {
		return err
	}

	return <-errChan
}

// streamHelpWithGlamour renders markdown help in alternate screen buffer (modal)
// Writes to /dev/tty to avoid interfering with stdout capture in shell integration
func streamHelpWithGlamour(output <-chan string) error {
	// Open TTY for all output (bypasses stdout capture)
	tty, err := getTTY()
	if err != nil {
		// Fallback: just drain the channel if no TTY
		for range output {
		}
		return nil
	}
	defer tty.Close()

	var content strings.Builder
	lastRendered := ""
	gotFirstChunk := false
	spinnerDone := make(chan struct{})
	termWidth := getHelpTerminalWidth()

	// Enter alternate screen buffer
	fmt.Fprint(tty, "\033[?1049h") // Enter alternate screen
	fmt.Fprint(tty, "\033[H")      // Move cursor to top-left
	defer fmt.Fprint(tty, "\033[?1049l") // Exit alternate screen

	// Start spinner
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		fmt.Fprintf(tty, "%s Generating help...", frames[i])
		for {
			select {
			case <-spinnerDone:
				return
			case <-ticker.C:
				i = (i + 1) % len(frames)
				fmt.Fprintf(tty, "\r%s Generating help...", frames[i])
			}
		}
	}()

	// Stream and render content
	for chunk := range output {
		if !gotFirstChunk {
			gotFirstChunk = true
			close(spinnerDone)
			time.Sleep(10 * time.Millisecond)
		}

		content.WriteString(chunk)

		// Re-render when we get a newline
		if strings.Contains(chunk, "\n") {
			rendered, err := renderHelpMarkdown(content.String(), termWidth)
			if err != nil {
				rendered = content.String()
			}

			if rendered != lastRendered {
				fmt.Fprint(tty, "\033[H\033[J") // Clear and redraw
				fmt.Fprint(tty, rendered)
				lastRendered = rendered
			}
		}
	}

	// Stop spinner if we never got content
	if !gotFirstChunk {
		close(spinnerDone)
	}

	// Final render with footer
	finalContent := content.String()
	if finalContent != "" {
		rendered, err := renderHelpMarkdown(finalContent, termWidth)
		if err != nil {
			rendered = finalContent
		}
		fmt.Fprint(tty, "\033[H\033[J")
		fmt.Fprint(tty, rendered)

		// Add footer hint
		fmt.Fprintf(tty, "\n\033[90mPress any key to exit...\033[0m")
	}

	// Wait for any key before exiting alternate screen
	waitForAnyKey()
	return nil
}

// waitForAnyKey waits for user to press any key
func waitForAnyKey() {
	tty, err := getTTY()
	if err != nil {
		return
	}
	defer tty.Close()

	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		return
	}
	defer term.Restore(int(tty.Fd()), oldState)

	buf := make([]byte, 1)
	tty.Read(buf)
}

// getHelpTerminalWidth returns terminal width or a default
func getHelpTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80 // default
	}
	return width
}

// renderHelpMarkdown renders markdown content using glamour
func renderHelpMarkdown(content string, width int) (string, error) {
	style := styles.DraculaStyleConfig
	style.Document.Margin = uintPtr(0)
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.CodeBlock.Margin = uintPtr(0)

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}

	rendered, err := renderer.Render(content)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(rendered)
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	// Ensure text is wrapped to terminal width
	return wordwrap.String(result, width), nil
}

func uintPtr(v uint) *uint {
	return &v
}
