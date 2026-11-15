package video

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/providerhttp"
	"github.com/term-llm/term-llm/internal/runtimeoutput"
	"github.com/term-llm/term-llm/internal/venice"
)

const (
	veniceBaseURL               = "https://api.venice.ai/api/v1"
	veniceVideoQuoteEndpoint    = "/video/quote"
	veniceVideoQueueEndpoint    = "/video/queue"
	veniceVideoRetrieveEndpoint = "/video/retrieve"
	veniceDefaultTextModel      = "longcat-distilled-text-to-video"
	veniceDefaultImageModel     = "longcat-distilled-image-to-video"
	veniceDefaultDuration       = "5s"
	veniceDefaultResolution     = ""
	veniceDefaultAspectRatio    = "16:9"
	veniceDefaultNegativePrompt = ""
	veniceDefaultPollInterval   = 5 * time.Second
	veniceDefaultTimeout        = mediautil.DefaultMediaTimeout
)

const (
	DefaultDuration       = veniceDefaultDuration
	DefaultResolution     = veniceDefaultResolution
	DefaultAspectRatio    = veniceDefaultAspectRatio
	DefaultNegativePrompt = veniceDefaultNegativePrompt
	DefaultPollInterval   = veniceDefaultPollInterval
	DefaultTimeout        = veniceDefaultTimeout
)

type InputImage struct {
	Path string
	Data []byte
	URL  string
}

type Request struct {
	Prompt          string
	Model           string
	Duration        string
	AspectRatio     string
	Resolution      *string
	Audio           *bool
	NegativePrompt  *string
	SeedanceConsent bool
	ImageURL        string
	ImagePath       string
	ImageData       []byte
	ReferenceImages []InputImage
	Debug           bool
	DebugRaw        bool
}

type Quote struct {
	Amount float64
}

type QueuedJob struct {
	Model   string
	QueueID string
}

type Retrieval struct {
	Done                 bool
	Status               string
	AverageExecutionTime int64
	ExecutionDuration    int64
	Data                 []byte
	MimeType             string
}

type VeniceProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewVeniceProvider(apiKey string) *VeniceProvider {
	return &VeniceProvider{
		apiKey:  config.NormalizeVeniceAPIKey(apiKey),
		baseURL: veniceBaseURL,
		client:  &http.Client{CheckRedirect: refuseRedirect},
	}
}

func (p *VeniceProvider) Quote(ctx context.Context, req Request) (*Quote, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()

	payload, err := p.options(req)
	if err != nil {
		return nil, err
	}

	var respBody struct {
		Quote *float64 `json:"quote"`
	}
	if err := p.doJSON(ctx, http.MethodPost, veniceVideoQuoteEndpoint, payload, req.Debug || req.DebugRaw, &respBody); err != nil {
		return nil, err
	}
	if respBody.Quote == nil || math.IsNaN(*respBody.Quote) || math.IsInf(*respBody.Quote, 0) || *respBody.Quote < 0 {
		return nil, fmt.Errorf("invalid Venice quote response")
	}
	return &Quote{Amount: *respBody.Quote}, nil
}

func (p *VeniceProvider) Queue(ctx context.Context, req Request) (*QueuedJob, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()

	payload, err := p.options(req)
	if err != nil {
		return nil, err
	}
	payload["prompt"] = req.Prompt
	if req.NegativePrompt != nil {
		payload["negative_prompt"] = *req.NegativePrompt
	}
	if req.SeedanceConsent {
		payload["consents"] = map[string]any{"seedance": map[string]bool{
			"confirmed_terms_and_privacy": true, "confirmed_legal_right": true, "confirmed_screening_acknowledged": true,
		}}
	}
	if req.ImageURL != "" {
		payload["image_url"] = req.ImageURL
	} else if len(req.ImageData) > 0 {
		payload["image_url"] = dataURL(req.ImagePath, req.ImageData)
	}
	if len(req.ReferenceImages) > 0 {
		references := make([]string, 0, len(req.ReferenceImages))
		for _, img := range req.ReferenceImages {
			if img.URL != "" {
				references = append(references, img.URL)
			} else {
				references = append(references, dataURL(img.Path, img.Data))
			}
		}
		payload["reference_image_urls"] = references
	}

	var respBody struct {
		Model   string `json:"model"`
		QueueID string `json:"queue_id"`
	}
	if err := p.doJSON(ctx, http.MethodPost, veniceVideoQueueEndpoint, payload, req.Debug || req.DebugRaw, &respBody); err != nil {
		return nil, err
	}
	if respBody.Model == "" {
		respBody.Model = req.Model
	}
	if !validIdentifier(respBody.Model) || !validIdentifier(respBody.QueueID) {
		return nil, fmt.Errorf("invalid Venice queue receipt; outcome unknown: a job may exist, do not requeue")
	}
	return &QueuedJob{Model: respBody.Model, QueueID: respBody.QueueID}, nil
}

// Retrieve preserves the old buffered API for callers, bounded to image-sized data.
// CLI uses RetrieveTo, which streams up to MaxVideoBytes without buffering.
func (p *VeniceProvider) Retrieve(ctx context.Context, job QueuedJob, deleteMediaOnCompletion bool, debug bool) (*Retrieval, error) {
	if deleteMediaOnCompletion {
		return nil, fmt.Errorf("delete-on-retrieve is unsafe; use Complete only after a durable local save")
	}
	var dst bytes.Buffer
	result, err := p.retrieveTo(ctx, job, &dst, debug, 64<<20)
	if err == nil && result.Done {
		result.Data = dst.Bytes()
	}
	return result, err
}

const MaxVideoBytes int64 = 1 << 30
const maxJSONBytes int64 = 8 << 20

// RetrieveTo never deletes remote media and streams a successful response to dst.
func (p *VeniceProvider) RetrieveTo(ctx context.Context, job QueuedJob, dst io.Writer, debug bool) (*Retrieval, error) {
	return p.retrieveTo(ctx, job, dst, debug, MaxVideoBytes)
}

func (p *VeniceProvider) retrieveTo(ctx context.Context, job QueuedJob, dst io.Writer, debug bool, limit int64) (*Retrieval, error) {
	if !validIdentifier(job.Model) || !validIdentifier(job.QueueID) {
		return nil, fmt.Errorf("model and queue_id are required valid identifiers")
	}
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	opts := p.requestOptions(http.MethodPost, veniceVideoRetrieveEndpoint, map[string]any{"model": job.Model, "queue_id": job.QueueID, "delete_media_on_completion": false}, debug)
	resp, err := providerhttp.OpenJSONRequest(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := providerhttp.ReadBounded(resp.Body, maxJSONBytes)
		if err != nil {
			return nil, err
		}
		return nil, venice.StatusError(resp, body, p.apiKey)
	}
	reader := bufio.NewReader(resp.Body)
	head, _ := reader.Peek(512)
	contentType := resp.Header.Get("Content-Type")
	if isLikelyJSON(contentType, bytes.TrimSpace(head)) {
		body, err := providerhttp.ReadBounded(reader, maxJSONBytes)
		if err != nil {
			return nil, err
		}
		var status struct {
			Status               string `json:"status"`
			AverageExecutionTime int64  `json:"average_execution_time"`
			ExecutionDuration    int64  `json:"execution_duration"`
		}
		if err := json.Unmarshal(body, &status); err != nil {
			return nil, fmt.Errorf("parse Venice retrieve response: %w", err)
		}
		if strings.TrimSpace(status.Status) == "" {
			return nil, fmt.Errorf("Venice retrieve response missing status")
		}
		if TerminalFailure(status.Status) {
			return nil, fmt.Errorf("Venice video job failed: %s", status.Status)
		}
		return &Retrieval{Status: status.Status, AverageExecutionTime: status.AverageExecutionTime, ExecutionDuration: status.ExecutionDuration}, nil
	}
	mimeType := strings.TrimSpace(strings.Split(contentType, ";")[0])
	if len(head) == 0 || (!strings.HasPrefix(strings.ToLower(mimeType), "video/") && !(len(head) >= 12 && string(head[4:8]) == "ftyp")) {
		return nil, fmt.Errorf("Venice retrieve returned neither a video nor a valid status")
	}
	n, err := io.Copy(dst, io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("stream Venice video: %w", err)
	}
	if n > limit {
		return nil, fmt.Errorf("video exceeds %d byte limit", limit)
	}
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return &Retrieval{Done: true, Status: "completed", MimeType: mimeType}, nil
}

// Complete uses the documented delete-only endpoint; call only after saving.
// https://docs.venice.ai/api-reference/endpoint/video/complete
func (p *VeniceProvider) Complete(ctx context.Context, job QueuedJob) error {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	var result struct {
		Success bool `json:"success"`
	}
	if err := p.doJSON(ctx, http.MethodPost, "/video/complete", map[string]string{"model": job.Model, "queue_id": job.QueueID}, false, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("Venice cleanup did not confirm success")
	}
	return nil
}

// TerminalFailure treats FAILED conservatively as terminal (offline assumption).
// Static retrieve docs enumerate only PROCESSING/COMPLETED; unknown statuses poll.
func TerminalFailure(status string) bool { return strings.EqualFold(status, "failed") }

func (p *VeniceProvider) doJSON(ctx context.Context, method, endpoint string, payload any, debug bool, out any) error {
	body, _, err := p.do(ctx, method, endpoint, payload, debug)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse Venice response: %w", err)
	}
	return nil
}

func (p *VeniceProvider) do(ctx context.Context, method, endpoint string, payload any, debug bool) ([]byte, string, error) {
	body, contentType, err := providerhttp.DoJSONRequest(ctx, p.requestOptions(method, endpoint, payload, debug))
	var status *providerhttp.StatusError
	if errors.As(err, &status) {
		err = venice.SanitizeStatusError(status, p.apiKey)
	}
	return body, contentType, err
}

func ResolveModel(model string, hasInput bool) string {
	if model != "" {
		return model
	}
	if hasInput {
		return veniceDefaultImageModel
	}
	return veniceDefaultTextModel
}

func ValidateDuration(duration string) error {
	value, err := strconv.ParseFloat(strings.TrimSuffix(duration, "s"), 64)
	if !durationSyntax.MatchString(duration) || err != nil || value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return fmt.Errorf("invalid duration (expected positive seconds, e.g. 5s)")
	}
	return nil
}

func ValidateResolution(resolution string) error {
	if !resolutionSyntax.MatchString(resolution) {
		return fmt.Errorf("invalid resolution (expected like 720p or 4k)")
	}
	return nil
}

func ValidateAspectRatio(aspectRatio string) error {
	if strings.TrimSpace(aspectRatio) == "" {
		return nil
	}
	parts := strings.Split(aspectRatio, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid aspect ratio %q (expected like 16:9)", aspectRatio)
	}
	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid aspect ratio (expected positive W:H)")
		}
	}
	return nil
}

func LoadInputImage(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input image %s: %w", path, err)
	}
	return data, nil
}

func SaveVideo(data []byte, outputDir, prompt string) (string, error) {
	return mediautil.SaveFile(data, outputDir, "", generateVideoFilename(prompt), "video")
}

func generateVideoFilename(prompt string) string {
	timestamp := time.Now().Format("20060102-150405")
	safe := mediautil.SanitizeFilename(prompt)
	if len(safe) > 30 {
		safe = safe[:30]
	}
	if safe == "" {
		safe = "video"
	}
	return fmt.Sprintf("%s-%s.mp4", timestamp, safe)
}

func dataURL(path string, data []byte) string {
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
}

func isLikelyJSON(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "json") {
		return true
	}
	return len(body) > 0 && (body[0] == '{' || body[0] == '[')
}

func veniceRequestError(err error) error {
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("Venice request timed out; outcome unknown: %w", err)
	}
	return fmt.Errorf("Venice request failed: %w", err)
}

func debugLog(title, format string, args ...any) {
	runtimeoutput.Printf("\n=== %s ===\n%s\n", title, fmt.Sprintf(format, args...))
}

func refuseRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func (p *VeniceProvider) requestOptions(method, endpoint string, payload any, debug bool) providerhttp.JSONRequestOptions {
	client := *p.client
	client.CheckRedirect = refuseRedirect
	return providerhttp.JSONRequestOptions{
		Client: &client, Method: method, URL: p.baseURL + endpoint, APIKey: p.apiKey, Payload: payload,
		Provider: "Venice", WrapRequestError: veniceRequestError, Debug: debug, Debugf: debugLog,
		RequestLabel: "Venice Request", ResponseLabel: "Venice Response", MaxResponseBytes: maxJSONBytes, RedactPayload: true,
	}
}

func (p *VeniceProvider) options(req Request) (map[string]any, error) {
	duration := req.Duration
	if duration == "" {
		duration = DefaultDuration
	}
	if err := p.ValidateRequest(req); err != nil {
		return nil, err
	}
	payload := map[string]any{"model": req.Model, "duration": duration}
	if req.Resolution != nil {
		payload["resolution"] = *req.Resolution
	}
	if req.Audio != nil {
		payload["audio"] = *req.Audio
	}
	if req.AspectRatio != "" {
		payload["aspect_ratio"] = req.AspectRatio
	}
	return payload, nil
}

var durationSyntax = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?s$`)
var resolutionSyntax = regexp.MustCompile(`^[1-9][0-9]*(p|k)$`)

func validIdentifier(s string) bool {
	return s != "" && len(s) <= 512 && !strings.ContainsAny(s, "\x00\r\n")
}
