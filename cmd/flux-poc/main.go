package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	generateEndpoint = "https://api.bfl.ai/v1/flux-2-pro"
	kontextEndpoint  = "https://api.bfl.ai/v1/flux-kontext-pro"
	defaultPrompt    = "A cute robot cat sitting on a rainbow, digital art style"
)

// Generation request (text-to-image)
type GenerateRequest struct {
	Prompt      string `json:"prompt"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

// Kontext request (image editing)
type KontextRequest struct {
	Prompt       string `json:"prompt"`
	InputImage   string `json:"input_image,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

// Initial response with polling URL
type TaskResponse struct {
	ID         string `json:"id"`
	PollingURL string `json:"polling_url"`
}

// Polling result
type PollResponse struct {
	Status string      `json:"status"`
	Result *PollResult `json:"result,omitempty"`
}

type PollResult struct {
	Sample string `json:"sample"` // URL to the generated image
}

func getMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func main() {
	var inputImage string
	var prompt string
	flag.StringVar(&inputImage, "input", "", "Path to input image to edit (uses Kontext)")
	flag.StringVar(&prompt, "prompt", defaultPrompt, "Text prompt for generation")
	flag.Parse()

	apiKey := os.Getenv("BFL_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "BFL_API_KEY environment variable not set")
		os.Exit(1)
	}

	var pollingURL string
	var err error

	if inputImage != "" {
		pollingURL, err = submitKontextRequest(apiKey, inputImage, prompt)
	} else {
		pollingURL, err = submitGenerateRequest(apiKey, prompt)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Poll for result
	imageURL, err := pollForResult(apiKey, pollingURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error polling: %v\n", err)
		os.Exit(1)
	}

	// Download the image
	imageData, err := downloadImage(imageURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading: %v\n", err)
		os.Exit(1)
	}

	outputPath := "/tmp/flux-test.png"
	if err := os.WriteFile(outputPath, imageData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write image: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Image saved to: %s (%d bytes)\n", outputPath, len(imageData))
}

func submitGenerateRequest(apiKey, prompt string) (string, error) {
	fmt.Printf("Generating image with prompt: %q\n", prompt)
	fmt.Printf("Using endpoint: %s\n", generateEndpoint)

	req := GenerateRequest{
		Prompt:      prompt,
		AspectRatio: "1:1",
	}

	return submitRequest(apiKey, generateEndpoint, req)
}

func submitKontextRequest(apiKey, imagePath, prompt string) (string, error) {
	fmt.Printf("Input image: %s\n", imagePath)
	fmt.Printf("Editing with prompt: %q\n", prompt)
	fmt.Printf("Using endpoint: %s\n", kontextEndpoint)

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read input image: %w", err)
	}
	fmt.Printf("Input image size: %d bytes\n", len(imageData))

	// Create data URI
	mimeType := getMimeType(imagePath)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imageData))

	req := KontextRequest{
		Prompt:       prompt,
		InputImage:   dataURI,
		OutputFormat: "png",
	}

	return submitRequest(apiKey, kontextEndpoint, req)
}

func submitRequest(apiKey, endpoint string, reqBody any) (string, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var taskResp TaskResponse
	if err := json.Unmarshal(body, &taskResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Printf("Task ID: %s\n", taskResp.ID)
	return taskResp.PollingURL, nil
}

func pollForResult(apiKey, pollingURL string) (string, error) {
	fmt.Print("Waiting for result")

	client := &http.Client{}
	maxAttempts := 120 // 2 minutes max
	for i := 0; i < maxAttempts; i++ {
		httpReq, err := http.NewRequest("GET", pollingURL, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create poll request: %w", err)
		}
		httpReq.Header.Set("x-key", apiKey)

		resp, err := client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("poll request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read poll response: %w", err)
		}

		var pollResp PollResponse
		if err := json.Unmarshal(body, &pollResp); err != nil {
			return "", fmt.Errorf("failed to parse poll response: %w", err)
		}

		switch pollResp.Status {
		case "Ready":
			fmt.Println(" done!")
			if pollResp.Result == nil || pollResp.Result.Sample == "" {
				return "", fmt.Errorf("no image URL in result")
			}
			return pollResp.Result.Sample, nil
		case "Pending", "Processing":
			fmt.Print(".")
			time.Sleep(1 * time.Second)
		default:
			return "", fmt.Errorf("unexpected status: %s (body: %s)", pollResp.Status, string(body))
		}
	}

	return "", fmt.Errorf("timeout waiting for result")
}

func downloadImage(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
