package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ReadText reads text content from the system clipboard
func ReadText() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return readTextMacOS()
	case "linux":
		return readTextLinux()
	default:
		return "", fmt.Errorf("clipboard read not supported on %s", runtime.GOOS)
	}
}

func readTextMacOS() (string, error) {
	cmd := exec.Command("pbpaste")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to read clipboard: %w", err)
	}
	return out.String(), nil
}

func readTextLinux() (string, error) {
	// Try wl-paste first (Wayland)
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command("wl-paste", "--no-newline")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			return out.String(), nil
		}
	}

	// Fall back to xclip (X11)
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-o")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			return out.String(), nil
		}
	}

	return "", fmt.Errorf("no clipboard utility found (install wl-paste or xclip)")
}

// ReadPrimarySelection reads from the PRIMARY selection (middle-click buffer on Linux).
// On macOS, falls back to the regular clipboard since there's no primary selection.
func ReadPrimarySelection() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return readTextMacOS() // macOS has no primary selection concept
	case "linux":
		return readPrimaryLinux()
	default:
		return "", fmt.Errorf("primary selection not supported on %s", runtime.GOOS)
	}
}

func readPrimaryLinux() (string, error) {
	// Try wl-paste --primary first (Wayland)
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command("wl-paste", "--primary", "--no-newline")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			return out.String(), nil
		}
	}

	// Fall back to xclip with primary selection (X11)
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "primary", "-o")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			return out.String(), nil
		}
	}

	return "", fmt.Errorf("no clipboard utility found (install wl-paste or xclip)")
}

// ReadImage reads image data from the system clipboard
// Returns the image data and an error if clipboard doesn't contain an image
func ReadImage() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return readImageMacOS()
	case "linux":
		return readImageLinux()
	default:
		return nil, fmt.Errorf("clipboard read not supported on %s", runtime.GOOS)
	}
}

func readImageMacOS() ([]byte, error) {
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

func readImageLinux() ([]byte, error) {
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

// CopyText copies text to the system clipboard
func CopyText(text string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "linux":
		return copyTextLinux(text)
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

func copyTextLinux(text string) error {
	// Try wl-copy first (Wayland)
	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}

	// Fall back to xclip (X11)
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}

	return fmt.Errorf("no clipboard utility found (install wl-copy or xclip)")
}

// CopyImage copies image data to the system clipboard
func CopyImage(imagePath string, imageData []byte) error {
	switch runtime.GOOS {
	case "darwin":
		return copyImageMacOS(imagePath)
	case "linux":
		return copyImageLinux(imageData)
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

func copyImageMacOS(imagePath string) error {
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

func copyImageLinux(imageData []byte) error {
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
