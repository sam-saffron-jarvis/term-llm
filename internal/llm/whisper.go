package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type TranscribeOptions struct {
	APIKey   string
	Endpoint string // full URL, e.g. "http://localhost:8080/inference" or "https://api.mistral.ai/v1/audio/transcriptions"
	Model    string // optional, overrides default model name sent to API
	Language string // optional, e.g. "en"
}

type whisperResponse struct {
	Text string `json:"text"`
}

const whisperErrorBodyLimit = 64 << 10

// TranscribeFile sends an audio file to a Whisper-compatible API and returns the transcript.
// Supported formats: flac, mp3, mp4, mpeg, mpga, m4a, ogg, wav, webm.
func TranscribeFile(ctx context.Context, filePath string, opts TranscribeOptions) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open audio file: %w", err)
	}
	defer f.Close()

	model := opts.Model
	if model == "" {
		model = "whisper-1"
	}

	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/transcriptions"
	}

	bodyReader, contentType := newTranscriptionBody(filePath, f, model, opts.Language)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bodyReader)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper API error %d: %s", resp.StatusCode, readLimitedBody(resp.Body, whisperErrorBodyLimit))
	}

	var result whisperResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode whisper response: %w", err)
	}

	return result.Text, nil
}

func newTranscriptionBody(filePath string, file io.Reader, model, language string) (io.Reader, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		err := writeTranscriptionBody(mw, filePath, file, model, language)
		_ = pw.CloseWithError(err)
	}()

	return pr, mw.FormDataContentType()
}

func writeTranscriptionBody(mw *multipart.Writer, filePath string, file io.Reader, model, language string) (err error) {
	defer func() {
		closeErr := mw.Close()
		if err == nil {
			err = closeErr
		}
	}()

	fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, file); err != nil {
		return fmt.Errorf("write form file: %w", err)
	}
	if err := mw.WriteField("model", model); err != nil {
		return fmt.Errorf("write model field: %w", err)
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return fmt.Errorf("write response format field: %w", err)
	}
	if language != "" {
		if err := mw.WriteField("language", language); err != nil {
			return fmt.Errorf("write language field: %w", err)
		}
	}
	return nil
}

func readLimitedBody(r io.Reader, limit int64) string {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return fmt.Sprintf("failed to read error body: %v", err)
	}
	if int64(len(b)) > limit {
		return string(b[:limit]) + "...[truncated]"
	}
	return string(b)
}
