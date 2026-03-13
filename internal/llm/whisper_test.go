package llm

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscribeFile_StreamsMultipartBody(t *testing.T) {
	origClient := defaultHTTPClient
	defer func() {
		defaultHTTPClient = origClient
	}()

	audioFile, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	if _, err := audioFile.WriteString("audio-bytes"); err != nil {
		t.Fatalf("write temp audio: %v", err)
	}
	if err := audioFile.Close(); err != nil {
		t.Fatalf("close temp audio: %v", err)
	}

	var (
		contentLength int64
		contentType   string
		bodyBytes     []byte
	)
	defaultHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			contentLength = req.ContentLength
			contentType = req.Header.Get("Content-Type")
			bodyBytes, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"text":"ok"}`)),
			}, nil
		}),
	}

	text, err := TranscribeFile(context.Background(), audioFile.Name(), TranscribeOptions{
		Endpoint: "https://transcribe.example.test/v1/audio/transcriptions",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("TranscribeFile failed: %v", err)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want %q", text, "ok")
	}
	if contentLength > 0 {
		t.Fatalf("ContentLength = %d, want streamed request body", contentLength)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType failed: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("media type = %q, want multipart/form-data", mediaType)
	}

	reader := multipart.NewReader(bytes.NewReader(bodyBytes), params["boundary"])
	fields := map[string]string{}
	fileName := ""
	fileBody := ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart failed: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll part failed: %v", err)
		}
		if part.FormName() == "file" {
			fileName = part.FileName()
			fileBody = string(data)
			continue
		}
		fields[part.FormName()] = string(data)
	}

	if fileName != filepath.Base(audioFile.Name()) {
		t.Fatalf("file name = %q, want %q", fileName, filepath.Base(audioFile.Name()))
	}
	if fileBody != "audio-bytes" {
		t.Fatalf("file body = %q, want %q", fileBody, "audio-bytes")
	}
	if fields["model"] != "whisper-1" {
		t.Fatalf("model = %q, want %q", fields["model"], "whisper-1")
	}
	if fields["response_format"] != "json" {
		t.Fatalf("response_format = %q, want %q", fields["response_format"], "json")
	}
	if fields["language"] != "en" {
		t.Fatalf("language = %q, want %q", fields["language"], "en")
	}
}

func TestTranscribeFile_LimitsErrorBody(t *testing.T) {
	origClient := defaultHTTPClient
	defer func() {
		defaultHTTPClient = origClient
	}()

	audioFile, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	if _, err := audioFile.WriteString("audio-bytes"); err != nil {
		t.Fatalf("write temp audio: %v", err)
	}
	if err := audioFile.Close(); err != nil {
		t.Fatalf("close temp audio: %v", err)
	}

	defaultHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", whisperErrorBodyLimit+128))),
			}, nil
		}),
	}

	_, err = TranscribeFile(context.Background(), audioFile.Name(), TranscribeOptions{
		Endpoint: "https://transcribe.example.test/v1/audio/transcriptions",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "...[truncated]") {
		t.Fatalf("error = %q, want truncated marker", err)
	}
	if len(err.Error()) > whisperErrorBodyLimit+128 {
		t.Fatalf("error length = %d, want capped size", len(err.Error()))
	}
}
