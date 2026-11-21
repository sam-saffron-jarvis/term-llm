package serve

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/app/uploads"
)

// Exercise the actual web-v1 request/frame budget, not localhost HTTP with a
// synthetic device principal. A phone photo cannot ride in the prompt frame.
func TestLinkedComposerPhotoAcrossPeer(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	client, closePeer := mountedPeerServeFixture(t, "/chat")
	defer closePeer()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	random := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = byte(random.Intn(256))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if len(raw) < 128<<10 {
		t.Fatalf("fixture too small: %d", len(raw))
	}
	const chunk = 32 << 10
	var ref uploads.Reference
	var token string
	for offset := 0; offset < len(raw); offset += chunk {
		end := min(offset+chunk, len(raw))
		req, err := http.NewRequest(http.MethodPost, "http://peer/chat/v1/linked-uploads", bytes.NewReader(raw[offset:end]))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "image/png")
		req.Header.Set("X-Term-LLM-Upload-Offset", strconv.Itoa(offset))
		if offset == 0 {
			req.Header.Set(uploads.FilenameHeader, "camera.png")
			req.Header.Set("X-Term-LLM-Upload-Size", strconv.Itoa(len(raw)))
		} else {
			req.Header.Set("X-Term-LLM-Upload-Token", token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if end == len(raw) {
			err = json.NewDecoder(resp.Body).Decode(&ref)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 201 || ref.SizeBytes != int64(len(raw)) {
				t.Fatalf("finish upload: HTTP %d, ref=%+v, err=%v", resp.StatusCode, ref, err)
			}
		} else {
			var state struct{ Token string }
			err = json.NewDecoder(resp.Body).Decode(&state)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 202 || state.Token == "" {
				t.Fatalf("acknowledge chunk: HTTP %d, err=%v", resp.StatusCode, err)
			}
			token = state.Token
		}
	}
	_, _, saved, err := uploads.ResolveReference(ref.UploadID)
	if err != nil || !bytes.Equal(raw, saved) {
		t.Fatalf("stored photo did not round-trip: %v", err)
	}
	prompt, err := json.Marshal(map[string]any{"stream": true, "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_upload", "upload_id": ref.UploadID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	response := assertPeerResponse(t, client, http.MethodPost, "/chat/v1/responses", bytes.NewReader(prompt), http.StatusOK, "text/event-stream", "response.completed")
	if strings.Contains(response, "event: response.failed") {
		t.Fatal("chat send failed")
	}
}
