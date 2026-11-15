package image

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/term-llm/term-llm/internal/mediautil"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
)

const (
	veniceJSONLimit  int64 = 8 << 20
	veniceImageLimit int64 = 64 << 20
	// Base64 encodes 3 raw bytes in 4 characters. Allow JSON/metadata slack,
	// then independently enforce the decoded image bound before allocation.
	veniceEncodedImageLimit int64 = ((veniceImageLimit+2)/3)*4 + (1 << 20)
)

func readVeniceBody(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("Venice response exceeds %d bytes", limit)
	}
	return b, nil
}

func (p *VeniceProvider) callEditModel(req EditRequest) string {
	if req.Model != "" {
		return req.Model
	}
	return p.editModel()
}

func (p *VeniceProvider) multipartRequest(ctx context.Context, endpoint string, img InputImage, fields map[string]string) (*http.Request, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, filepath.Base(img.Path)))
	h.Set("Content-Type", getMimeType(img.Path))
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("create image part: %w", err)
	}
	if _, err := part.Write(img.Data); err != nil {
		return nil, fmt.Errorf("write image part: %w", err)
	}
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s: %w", key, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	return req, nil
}

func (p *VeniceProvider) singleURLEdit(ctx context.Context, req EditRequest, options veniceImageOptions) (*ImageResult, error) {
	payload := struct {
		veniceImageOptions
		Model  string `json:"modelId"`
		Prompt string `json:"prompt"`
		Image  string `json:"image"`
		Format string `json:"output_format"`
	}{options, p.callEditModel(req), req.Prompt, req.InputImages[0].URL, "png"}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal URL edit: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/image/edit", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create URL edit request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	return p.doRawRequest(httpReq, req.Debug || req.DebugRaw)
}

func validateLocalProcessingImage(img InputImage) error {
	if img.URL != "" || mediautil.IsRemoteReference(img.Path) {
		return fmt.Errorf("processing requires a local image")
	}
	if len(img.Data) == 0 {
		return fmt.Errorf("processing requires image data")
	}
	return nil
}

func (p *VeniceProvider) Upscale(ctx context.Context, req UpscaleRequest) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	if err := validateLocalProcessingImage(req.Image); err != nil {
		return nil, err
	}
	if req.Scale != nil && *req.Scale <= 0 {
		return nil, fmt.Errorf("scale must be positive")
	}
	for _, v := range []*float64{req.EnhanceCreativity, req.Replication} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return nil, fmt.Errorf("processing options must be finite")
		}
	}
	fields := map[string]string{}
	if req.Scale != nil {
		fields["scale"] = strconv.Itoa(*req.Scale)
	}
	if req.Enhance != nil {
		fields["enhance"] = strconv.FormatBool(*req.Enhance)
	}
	if req.EnhancePrompt != nil {
		fields["enhancePrompt"] = *req.EnhancePrompt
	}
	if req.EnhanceCreativity != nil {
		fields["enhanceCreativity"] = strconv.FormatFloat(*req.EnhanceCreativity, 'f', -1, 64)
	}
	if req.Replication != nil {
		fields["replication"] = strconv.FormatFloat(*req.Replication, 'f', -1, 64)
	}
	httpReq, err := p.multipartRequest(ctx, "/image/upscale", req.Image, fields)
	if err != nil {
		return nil, err
	}
	return p.doRawRequest(httpReq, req.Debug || req.DebugRaw)
}

func (p *VeniceProvider) RemoveBackground(ctx context.Context, req BackgroundRemovalRequest) (*ImageResult, error) {
	ctx, cancel := mediautil.WithMediaTimeout(ctx)
	defer cancel()
	if err := validateLocalProcessingImage(req.Image); err != nil {
		return nil, err
	}
	httpReq, err := p.multipartRequest(ctx, "/image/background-remove", req.Image, nil)
	if err != nil {
		return nil, err
	}
	return p.doRawRequest(httpReq, req.Debug || req.DebugRaw)
}

// decodeVeniceImage bounds the decoded allocation as well as the encoded body.
func decodeVeniceImage(encoded string, limit int64) ([]byte, error) {
	n, padding := 0, 0
	for _, c := range encoded {
		if c == '\r' || c == '\n' {
			continue
		}
		n++
		if c == '=' {
			padding++
		}
	}
	if padding > 2 {
		return nil, fmt.Errorf("invalid image base64 padding; paid outcome unknown")
	}
	if int64(base64.StdEncoding.DecodedLen(n)-padding) > limit {
		return nil, fmt.Errorf("decoded Venice image exceeds %d bytes; paid outcome unknown", limit)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode Venice image; paid outcome unknown: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("decoded Venice image exceeds %d bytes; paid outcome unknown", limit)
	}
	return data, nil
}
