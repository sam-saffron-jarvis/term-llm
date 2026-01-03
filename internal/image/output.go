package image

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
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
	switch runtime.GOOS {
	case "darwin":
		return copyToClipboardMacOS(imagePath)
	case "linux":
		return copyToClipboardLinux(imageData)
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

func copyToClipboardMacOS(imagePath string) error {
	// Use osascript to copy image data to clipboard
	// This allows pasting the actual image in apps like Slack, etc.
	script := fmt.Sprintf(`set the clipboard to (read (POSIX file "%s") as TIFF picture)`, imagePath)
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		// Fallback: copy path as text using pbcopy
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(imagePath)
		return cmd.Run()
	}
	return nil
}

func copyToClipboardLinux(imageData []byte) error {
	// Try wl-copy first (Wayland)
	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy", "--type", "image/png")
		cmd.Stdin = bytes.NewReader(imageData)
		return cmd.Run()
	}

	// Fall back to xclip (X11)
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png")
		cmd.Stdin = bytes.NewReader(imageData)
		return cmd.Run()
	}

	return fmt.Errorf("no clipboard utility found (install wl-copy or xclip)")
}

// ReadFromClipboard reads image data from the system clipboard
// Returns the image data and an error if clipboard doesn't contain an image
func ReadFromClipboard() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardMacOS()
	case "linux":
		return readClipboardLinux()
	default:
		return nil, fmt.Errorf("clipboard read not supported on %s", runtime.GOOS)
	}
}

func readClipboardMacOS() ([]byte, error) {
	// Try pngpaste first (clean PNG output)
	if pngpastePath, err := exec.LookPath("pngpaste"); err == nil {
		// pngpaste outputs to file, use temp file
		tmpFile, err := os.CreateTemp("", "clipboard-*.png")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		cmd := exec.Command(pngpastePath, tmpPath)
		if err := cmd.Run(); err == nil {
			data, err := os.ReadFile(tmpPath)
			if err == nil && len(data) > 0 {
				return data, nil
			}
		}
	}

	// Fallback: use osascript to write clipboard to temp file, then convert with sips
	tmpTiff, err := os.CreateTemp("", "clipboard-*.tiff")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpTiffPath := tmpTiff.Name()
	tmpTiff.Close()
	defer os.Remove(tmpTiffPath)

	// Write clipboard as TIFF (most reliable format on macOS)
	script := fmt.Sprintf(`set tiffData to (the clipboard as «class TIFF»)
set fp to open for access POSIX file "%s" with write permission
write tiffData to fp
close access fp`, tmpTiffPath)

	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("clipboard does not contain an image")
	}

	// Check if file was written
	info, err := os.Stat(tmpTiffPath)
	if err != nil || info.Size() == 0 {
		return nil, fmt.Errorf("clipboard is empty or not an image")
	}

	// Convert TIFF to PNG using sips
	tmpPngPath := tmpTiffPath + ".png"
	defer os.Remove(tmpPngPath)

	cmd = exec.Command("sips", "-s", "format", "png", tmpTiffPath, "--out", tmpPngPath)
	cmd.Stderr = nil // suppress sips output
	if err := cmd.Run(); err != nil {
		// If sips fails, try returning the TIFF directly
		return os.ReadFile(tmpTiffPath)
	}

	data, err := os.ReadFile(tmpPngPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read converted image: %w", err)
	}

	return data, nil
}

func readClipboardLinux() ([]byte, error) {
	// Try wl-paste first (Wayland)
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command("wl-paste", "--type", "image/png")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.Bytes(), nil
		}
	}

	// Fall back to xclip (X11)
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.Bytes(), nil
		}
	}

	return nil, fmt.Errorf("clipboard does not contain an image (or no clipboard utility found)")
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
