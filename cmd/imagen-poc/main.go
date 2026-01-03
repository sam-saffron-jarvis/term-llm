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
)

const (
	// Gemini native image generation (Nano Banana)
	// Models that support image output:
	// - gemini-2.5-flash-image (stable)
	// - gemini-2.0-flash-exp (experimental)
	endpoint      = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-image:generateContent"
	defaultPrompt = "A cute robot cat sitting on a rainbow, digital art style"
)

type Request struct {
	Contents         []Content        `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig"`
}

type Content struct {
	Parts []Part `json:"parts"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GenerationConfig struct {
	ResponseModalities []string `json:"responseModalities"`
}

type Response struct {
	Candidates []Candidate `json:"candidates"`
	Error      *APIError   `json:"error,omitempty"`
}

type Candidate struct {
	Content ContentResponse `json:"content"`
}

type ContentResponse struct {
	Parts []PartResponse `json:"parts"`
}

type PartResponse struct {
	Text       string              `json:"text,omitempty"`
	InlineData *InlineDataResponse `json:"inlineData,omitempty"`
}

type InlineDataResponse struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func getMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func main() {
	var inputImage string
	var prompt string
	flag.StringVar(&inputImage, "input", "", "Path to input image to modify")
	flag.StringVar(&prompt, "prompt", defaultPrompt, "Text prompt for generation")
	flag.Parse()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "GEMINI_API_KEY environment variable not set")
		os.Exit(1)
	}

	var parts []Part

	// Add input image if provided
	if inputImage != "" {
		imageData, err := os.ReadFile(inputImage)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read input image: %v\n", err)
			os.Exit(1)
		}
		parts = append(parts, Part{
			InlineData: &InlineData{
				MimeType: getMimeType(inputImage),
				Data:     base64.StdEncoding.EncodeToString(imageData),
			},
		})
		fmt.Printf("Input image: %s (%d bytes)\n", inputImage, len(imageData))
	}

	// Add text prompt
	parts = append(parts, Part{Text: prompt})

	req := Request{
		Contents: []Content{
			{
				Parts: parts,
			},
		},
		GenerationConfig: GenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
		},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generating image with prompt: %q\n", prompt)
	fmt.Printf("Using endpoint: %s\n", endpoint)

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read response: %v\n", err)
		os.Exit(1)
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "API error (status %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var apiResp Response
	if err := json.Unmarshal(body, &apiResp); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse response: %v\n", err)
		os.Exit(1)
	}

	if apiResp.Error != nil {
		fmt.Fprintf(os.Stderr, "API error: %s\n", apiResp.Error.Message)
		os.Exit(1)
	}

	if len(apiResp.Candidates) == 0 {
		fmt.Fprintln(os.Stderr, "No candidates in response")
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(body))
		os.Exit(1)
	}

	var imageData []byte
	var textResponse string

	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			textResponse = part.Text
		}
		if part.InlineData != nil {
			imageData, err = base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to decode image: %v\n", err)
				os.Exit(1)
			}
		}
	}

	if textResponse != "" {
		fmt.Printf("Model response: %s\n", textResponse)
	}

	if len(imageData) == 0 {
		fmt.Fprintln(os.Stderr, "No image data in response")
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(body))
		os.Exit(1)
	}

	outputPath := "/tmp/gemini-test.png"
	if err := os.WriteFile(outputPath, imageData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write image: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Image saved to: %s (%d bytes)\n", outputPath, len(imageData))
}
