package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/samsaffron/term-llm/internal/clipboard"
)

// SaveImage saves image data to the configured output directory
// Returns the path where the image was saved
func SaveImage(data []byte, outputDir, prompt string) (string, error) {
	dir := expandPath(outputDir)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	filename := generateFilename(prompt)
	path := filepath.Join(dir, filename)

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write image: %w", err)
	}

	return path, nil
}

// DisplayImage displays the image in terminal using icat if available
// Returns nil if icat is not available or fails (display is non-critical)
func DisplayImage(imagePath string) error {
	// Try kitten icat first (newer kitty)
	if kittenPath, err := exec.LookPath("kitten"); err == nil {
		cmd := exec.Command(kittenPath, "icat", imagePath)
		cmd.Stdout = os.Stdout
		// Suppress stderr - icat errors are non-critical
		cmd.Run()
		return nil
	}

	// Try icat directly
	if icatPath, err := exec.LookPath("icat"); err == nil {
		cmd := exec.Command(icatPath, imagePath)
		cmd.Stdout = os.Stdout
		// Suppress stderr - icat errors are non-critical
		cmd.Run()
		return nil
	}

	// icat not available - silent fail
	return nil
}

// CopyToClipboard copies image to clipboard (platform-aware)
// Attempts to copy actual image data, not just the path
func CopyToClipboard(imagePath string, imageData []byte) error {
	return clipboard.CopyImage(imagePath, imageData)
}

// ReadFromClipboard reads image data from the system clipboard
// Returns the image data and an error if clipboard doesn't contain an image
func ReadFromClipboard() ([]byte, error) {
	return clipboard.ReadImage()
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// generateFilename creates a filename from timestamp and sanitized prompt
func generateFilename(prompt string) string {
	timestamp := time.Now().Format("20060102-150405")
	safe := sanitizeForFilename(prompt)
	if len(safe) > 30 {
		safe = safe[:30]
	}
	if safe == "" {
		safe = "image"
	}
	return fmt.Sprintf("%s-%s.png", timestamp, safe)
}

// sanitizeForFilename removes/replaces characters unsafe for filenames
func sanitizeForFilename(s string) string {
	// Replace spaces with underscores
	s = strings.ReplaceAll(s, " ", "_")
	// Remove non-alphanumeric except underscore and hyphen
	reg := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	s = reg.ReplaceAllString(s, "")
	// Collapse multiple underscores
	reg = regexp.MustCompile(`_+`)
	s = reg.ReplaceAllString(s, "_")
	// Trim leading/trailing underscores
	s = strings.Trim(s, "_")
	return strings.ToLower(s)
}
