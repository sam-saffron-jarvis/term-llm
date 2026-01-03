package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

const (
	generateEndpoint = "https://api.openai.com/v1/images/generations"
	editEndpoint     = "https://api.openai.com/v1/images/edits"
	defaultPrompt    = "A cute robot cat sitting on a rainbow, digital art style"
)

// Generation request (JSON)
type GenerateRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Size         string `json:"size"`
	Quality      string `json:"quality"`
	OutputFormat string `json:"output_format"`
	N            int    `json:"n"`
}

// Response structure
type Response struct {
	Data  []ImageData `json:"data"`
	Error *APIError   `json:"error,omitempty"`
}

type ImageData struct {
	B64JSON string `json:"b64_json,omitempty"`
	URL     string `json:"url,omitempty"`
}

type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
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
	var model string
	flag.StringVar(&inputImage, "input", "", "Path to input image to edit")
	flag.StringVar(&prompt, "prompt", defaultPrompt, "Text prompt for generation")
	flag.StringVar(&model, "model", "gpt-image-1", "Model to use (gpt-image-1, gpt-image-1.5)")
	flag.Parse()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY environment variable not set")
		os.Exit(1)
	}

	var imageData []byte
	var err error

	if inputImage != "" {
		imageData, err = editImage(apiKey, model, inputImage, prompt)
	} else {
		imageData, err = generateImage(apiKey, model, prompt)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outputPath := "/tmp/gptimage-test.png"
	if err := os.WriteFile(outputPath, imageData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write image: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Image saved to: %s (%d bytes)\n", outputPath, len(imageData))
}

func generateImage(apiKey, model, prompt string) ([]byte, error) {
	fmt.Printf("Generating image with prompt: %q\n", prompt)
	fmt.Printf("Using model: %s\n", model)

	req := GenerateRequest{
		Model:        model,
		Prompt:       prompt,
		Size:         "1024x1024",
		Quality:      "auto",
		OutputFormat: "png",
		N:            1,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", generateEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	return doRequest(httpReq)
}

func editImage(apiKey, model, imagePath, prompt string) ([]byte, error) {
	fmt.Printf("Input image: %s\n", imagePath)
	fmt.Printf("Editing with prompt: %q\n", prompt)
	fmt.Printf("Using model: %s\n", model)

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read input image: %w", err)
	}
	fmt.Printf("Input image size: %d bytes\n", len(imageData))

	// Build multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add image file with proper mime type
	mimeType := getMimeType(imagePath)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="%s"`, filepath.Base(imagePath)))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(imageData); err != nil {
		return nil, fmt.Errorf("failed to write image data: %w", err)
	}

	// Add other fields
	writer.WriteField("model", model)
	writer.WriteField("prompt", prompt)
	writer.WriteField("size", "1024x1024")
	writer.WriteField("quality", "auto")
	writer.WriteField("output_format", "png")
	writer.WriteField("n", "1")

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	httpReq, err := http.NewRequest("POST", editEndpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	return doRequest(httpReq)
}

func doRequest(httpReq *http.Request) ([]byte, error) {
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResp Response
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("no image data in response")
	}

	// Try base64 data first, then URL
	if apiResp.Data[0].B64JSON != "" {
		imageData, err := base64.StdEncoding.DecodeString(apiResp.Data[0].B64JSON)
		if err != nil {
			return nil, fmt.Errorf("failed to decode image: %w", err)
		}
		return imageData, nil
	}

	if apiResp.Data[0].URL != "" {
		resp, err := http.Get(apiResp.Data[0].URL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch image URL: %w", err)
		}
		defer resp.Body.Close()
		imageData, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read image from URL: %w", err)
		}
		return imageData, nil
	}

	return nil, fmt.Errorf("no image data in response (neither b64_json nor url)")
}
