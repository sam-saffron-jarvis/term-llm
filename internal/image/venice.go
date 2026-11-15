package image

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/runtimeoutput"
	"github.com/term-llm/term-llm/internal/venice"
)

const (
	veniceBaseURL           = "https://api.venice.ai/api/v1"
	veniceGenerateEndpoint  = veniceBaseURL + "/image/generate"
	veniceEditEndpoint      = veniceBaseURL + "/image/edit"
	veniceMultiEditEndpoint = veniceBaseURL + "/image/multi-edit"
)

var (
	veniceDefaultModel      = config.DefaultImageVeniceModel
	veniceDefaultResolution = config.DefaultImageVeniceResolution
	veniceHTTPClient        = &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
)

// VeniceProvider implements ImageProvider using Venice AI's native image API.
// - Text-to-image: POST /image/generate (JSON, returns base64 in JSON)
// - Single image edit: POST /image/edit (multipart/form-data, returns raw image bytes)
// - Explicit quality or multiple inputs: POST /image/multi-edit (JSON, raw image bytes)
type VeniceProvider struct {
	apiKey     string
	model      string
	editMdl    string
	resolution string
	baseURL    string
}

func NewVeniceProvider(apiKey, model, editModel, resolution string) *VeniceProvider {
	apiKey = config.NormalizeVeniceAPIKey(apiKey)
	if model == "" {
		model = veniceDefaultModel
	}
	if resolution == "" {
		resolution = veniceDefaultResolution
	}
	return &VeniceProvider{
		apiKey:     apiKey,
		model:      model,
		editMdl:    editModel,
		resolution: resolution,
		baseURL:    veniceBaseURL,
	}
}

func (p *VeniceProvider) Name() string {
	return "Venice"
}

func (p *VeniceProvider) SupportsVeniceControls() bool { return true }

func (p *VeniceProvider) SupportsEdit() bool {
	return true
}

func (p *VeniceProvider) SupportsMultiImage() bool {
	return true
}

// editModel returns the model ID for edit endpoints.
// Uses the explicit edit_model config if set, otherwise appends "-edit" to
// the generate model (most Venice models follow "name" → "name-edit").
func (p *VeniceProvider) editModel() string {
	if p.editMdl != "" {
		return p.editMdl
	}
	if strings.HasSuffix(p.model, "-edit") {
		return p.model
	}
	return p.model + "-edit"
}

// Generate calls POST /image/generate — text-to-image.
// Response: JSON { "images": ["<base64>"] }
func (p *VeniceProvider) Generate(ctx context.Context, req GenerateRequest) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	if err := ValidateVeniceControls(p, req.Venice, req.Size, req.AspectRatio); err != nil {
		return nil, err
	}
	options, err := p.requestOptions(req.Size, req.AspectRatio, req.Quality)
	if err != nil {
		return nil, err
	}

	genReq := veniceGenerateRequest{
		veniceImageOptions: options,
		Model:              p.model,
		Prompt:             req.Prompt,
		SafeMode:           false,
		HideWatermark:      true,
		Format:             "png",
	}

	if c := req.Venice; c != nil {
		genReq.Width, genReq.Height, genReq.Steps = c.Width, c.Height, c.Steps
		genReq.CFGScale, genReq.NegativePrompt = c.CFGScale, c.NegativePrompt
		if c.SafeMode != nil {
			genReq.SafeMode = *c.SafeMode
		}
		if c.Width != nil {
			genReq.Resolution = ""
		}
	}

	jsonBody, err := json.Marshal(genReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	debugRaw := req.Debug || req.DebugRaw
	if debugRaw {
		debugRawImageLog(debugRaw, "Venice Request", "POST %s\n%s", p.baseURL+"/image/generate", string(jsonBody))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/image/generate", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	return p.doJSONRequest(httpReq, debugRaw)
}

// Edit dispatches to the appropriate endpoint:
// - 1 image with default quality → POST /image/edit (multipart/form-data)
// - Explicit quality or 2-3 images → POST /image/multi-edit (JSON)
// Both request PNG output; response bytes determine the actual MIME type.
func (p *VeniceProvider) Edit(ctx context.Context, req EditRequest) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	if len(req.InputImages) == 0 {
		return nil, fmt.Errorf("no input image provided")
	}
	if len(req.InputImages) > 3 {
		return nil, fmt.Errorf("Venice supports at most 3 input images, got %d", len(req.InputImages))
	}

	if err := ValidateEditInputs(p, req.InputImages); err != nil {
		return nil, err
	}
	options, err := p.requestOptions(req.Size, req.AspectRatio, req.Quality)
	if err != nil {
		return nil, err
	}
	// The single-edit endpoint rejects quality (in JSON and multipart) despite
	// its docs. Multi-edit accepts one input and honors quality; route explicitly
	// rather than silently dropping the option or retrying a billable request.
	if len(req.InputImages) == 1 && options.Quality == "" {
		if req.InputImages[0].URL != "" {
			return p.singleURLEdit(ctx, req, options)
		}
		return p.singleEdit(ctx, req, options)
	}
	return p.multiEdit(ctx, req, options)
}

// singleEdit calls POST /image/edit with multipart/form-data for edits using
// the model's default quality. Explicit quality uses multiEdit, even for one input.
func (p *VeniceProvider) singleEdit(ctx context.Context, req EditRequest, options veniceImageOptions) (*ImageResult, error) {
	img := req.InputImages[0]
	debugRaw := req.Debug || req.DebugRaw

	fields := map[string]string{
		"modelId": p.callEditModel(req), "prompt": req.Prompt,
		"resolution": options.Resolution, "aspect_ratio": options.AspectRatio, "output_format": "png",
	}
	for k, v := range fields {
		if v == "" && k != "prompt" {
			delete(fields, k)
		}
	}
	httpReq, err := p.multipartRequest(ctx, "/image/edit", img, fields)
	if err != nil {
		return nil, err
	}

	return p.doRawRequest(httpReq, debugRaw)
}

// multiEdit calls POST /image/multi-edit with JSON body.
// Images are sent as base64-encoded strings. Response contains raw image bytes.
func (p *VeniceProvider) multiEdit(ctx context.Context, req EditRequest, options veniceImageOptions) (*ImageResult, error) {
	debugRaw := req.Debug || req.DebugRaw

	images := make([]string, len(req.InputImages))
	for i, img := range req.InputImages {
		if img.URL != "" {
			images[i] = img.URL
		} else {
			images[i] = base64.StdEncoding.EncodeToString(img.Data)
		}
	}

	multiReq := veniceMultiEditRequest{
		veniceImageOptions: options,
		ModelID:            p.callEditModel(req),
		Images:             images,
		Prompt:             req.Prompt,
		OutputFormat:       "png",
	}

	jsonBody, err := json.Marshal(multiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Log truncated body (base64 images are huge)
	debugRawImageLog(debugRaw, "Venice Request", "POST %s\nmodelId=%s prompt=%q resolution=%s aspect_ratio=%s quality=%s images=%d (body %d bytes)",
		p.baseURL+"/image/multi-edit", p.callEditModel(req), req.Prompt, options.Resolution, options.AspectRatio, options.Quality, len(images), len(jsonBody))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/image/multi-edit", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	return p.doRawRequest(httpReq, debugRaw)
}

// doJSONRequest handles responses with JSON { "images": ["<base64>"] }
func (p *VeniceProvider) doJSONRequest(httpReq *http.Request, debugRaw bool) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(httpReq.Context())
	httpReq = httpReq.Clone(ctx)
	defer cancel()
	client := *veniceHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(httpReq)
	if err != nil {
		debugRawImageLog(debugRaw, "Venice Error", "request failed: %v", err)
		return nil, veniceRequestError(err)
	}
	defer resp.Body.Close()

	limit := veniceEncodedImageLimit
	if resp.StatusCode != http.StatusOK {
		limit = veniceJSONLimit
	}
	body, err := readVeniceBody(resp.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("Venice image response unavailable; paid outcome unknown: %w", err)
	}

	if debugRaw {
		debugRawImageLog(debugRaw, "Venice Response", "status=%d content-type=%s body_len=%d",
			resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, venice.StatusError(resp, body, p.apiKey)
	}

	var apiResp veniceGenerateResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResp.Images) == 0 {
		return nil, fmt.Errorf("no image data in response")
	}

	imageData, err := decodeVeniceImage(apiResp.Images[0], veniceImageLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}
	debugRawImageLog(debugRaw, "Venice Decoded", "image_bytes=%d", len(imageData))
	return veniceImageResult(imageData)
}

// doRawRequest handles responses that contain raw image bytes.
func (p *VeniceProvider) doRawRequest(httpReq *http.Request, debugRaw bool) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(httpReq.Context())
	httpReq = httpReq.Clone(ctx)
	defer cancel()
	client := *veniceHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(httpReq)
	if err != nil {
		debugRawImageLog(debugRaw, "Venice Error", "request failed: %v", err)
		return nil, veniceRequestError(err)
	}
	defer resp.Body.Close()

	limit := int64(veniceImageLimit)
	if resp.StatusCode != http.StatusOK || strings.Contains(resp.Header.Get("Content-Type"), "json") {
		limit = veniceJSONLimit
	}
	body, err := readVeniceBody(resp.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("Venice image response unavailable; paid outcome unknown: %w", err)
	}

	debugRawImageLog(debugRaw, "Venice Response", "status=%d content-type=%s body_len=%d",
		resp.StatusCode, resp.Header.Get("Content-Type"), len(body))

	if resp.StatusCode != http.StatusOK {
		if debugRaw {
			debugRawImageLog(debugRaw, "Venice Error Body", "[redacted; %d bytes]", len(body))
		}
		return nil, venice.StatusError(resp, body, p.apiKey)
	}

	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		var failure struct {
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		if json.Unmarshal(body, &failure) == nil {
			if failure.Message != "" {
				return nil, fmt.Errorf("Venice image error: %s", failure.Message)
			}
			if len(failure.Error) != 0 {
				return nil, fmt.Errorf("Venice image error: %s", truncateDebugBody(failure.Error, 1024))
			}
		}
	}
	return veniceImageResult(body)
}

// Venice can return JPEG even when PNG was requested. Detect the actual bytes
// so saving and subsequent edits use the right extension and MIME type.
func veniceImageResult(data []byte) (*ImageResult, error) {
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("Venice returned non-image data (%s)", mimeType)
	}
	return &ImageResult{Data: data, MimeType: mimeType}, nil
}

// veniceImageOptions is shared by generation and both edit transports.
// Omitted quality and aspect ratio let Venice use the selected model's defaults.
// Sampling settings (steps/cfg_scale) are deliberately left to the model, too.
type veniceImageOptions struct {
	Resolution  string `json:"resolution,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Quality     string `json:"quality,omitempty"`
}

func (p *VeniceProvider) requestOptions(size, aspectRatio, quality string) (veniceImageOptions, error) {
	switch quality {
	case "", "auto":
		quality = ""
	case "low", "medium", "high":
		// Only quality-aware models use this; other Venice models ignore it.
	default:
		return veniceImageOptions{}, fmt.Errorf("unsupported Venice image quality %q (valid: auto, low, medium, high)", quality)
	}
	if size == NativeSize {
		size = ""
	} else if size == "" {
		size = p.resolution
	}
	// Sizing is stable across catalog states. Always preserve explicit sizing
	// or the configured/default resolution; catalog expiry must not change cost
	// or silently discard a caller's requested size.
	return veniceImageOptions{Resolution: size, AspectRatio: aspectRatio, Quality: quality}, nil
}

// Venice API request/response types

type veniceGenerateRequest struct {
	Width          *int     `json:"width,omitempty"`
	Height         *int     `json:"height,omitempty"`
	Steps          *int     `json:"steps,omitempty"`
	CFGScale       *float64 `json:"cfg_scale,omitempty"`
	NegativePrompt *string  `json:"negative_prompt,omitempty"`
	veniceImageOptions
	Model         string `json:"model"`
	Prompt        string `json:"prompt"`
	SafeMode      bool   `json:"safe_mode"`
	HideWatermark bool   `json:"hide_watermark"`
	Format        string `json:"format"`
}

type veniceMultiEditRequest struct {
	veniceImageOptions
	ModelID      string   `json:"modelId"`
	Images       []string `json:"images"`
	Prompt       string   `json:"prompt"`
	OutputFormat string   `json:"output_format"`
}

type veniceGenerateResponse struct {
	Images []string `json:"images"`
}

// veniceRequestError returns a clean error message for HTTP client failures.
func veniceRequestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("Venice API request timed out; remote outcome may be uncertain: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("Venice API request cancelled; remote outcome may be uncertain: %w", err)
	}
	return fmt.Errorf("Venice API request failed: %w", err)
}

// debugRawImageLog prints a timestamped debug section to stderr.
func debugRawImageLog(enabled bool, label, format string, args ...interface{}) {
	if !enabled {
		return
	}
	ts := time.Now().Format(time.RFC3339Nano)
	body := fmt.Sprintf(format, args...)
	runtimeoutput.Printf("\n[%s] %s\n", ts, label)
	if body != "" {
		runtimeoutput.Printf("%s\n", body)
	}
	runtimeoutput.Printf("[%s] END %s\n\n", ts, label)
}

// truncateDebugBody returns a string representation of body, truncated to maxLen bytes.
// For binary data (non-UTF8/non-printable), returns a summary instead.
func truncateDebugBody(body []byte, maxLen int) string {
	if len(body) == 0 {
		return "(empty)"
	}
	// Check if it looks like text (JSON, HTML, etc.)
	isText := true
	checkLen := len(body)
	if checkLen > 512 {
		checkLen = 512
	}
	for _, b := range body[:checkLen] {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			isText = false
			break
		}
	}
	if !isText {
		return fmt.Sprintf("(binary data, %d bytes)", len(body))
	}
	s := string(body)
	if len(s) > maxLen {
		return s[:maxLen] + fmt.Sprintf("...[truncated, %d total bytes]", len(body))
	}
	return s
}
