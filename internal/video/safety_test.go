package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Synthetic catalog/model choices test serialization, not live compatibility.
func TestVideoOptionalWireShape(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			var payloads []map[string]any
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				payloads = append(payloads, body)
				if strings.HasSuffix(r.URL.Path, "quote") {
					fmt.Fprint(w, `{"quote":1}`)
				} else {
					fmt.Fprint(w, `{"model":"synthetic","queue_id":"job"}`)
				}
			}))
			defer s.Close()
			p := NewVeniceProvider("test-only")
			p.baseURL = s.URL + "/prefix"
			req := Request{Model: "synthetic", Duration: "6s", Prompt: "hello"}
			if explicit {
				req.Resolution = ptr("4k")
				req.Audio = ptr(false)
				req.NegativePrompt = ptr("")
				req.SeedanceConsent = true
				req.ImageURL = "https://example.org/main?signature=secret"
				req.ReferenceImages = []InputImage{{URL: "http://example.org/ref"}, {Path: "local.png", Data: []byte("png")}}
			}
			if _, err := p.Quote(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Queue(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			for _, body := range payloads {
				_, resolution := body["resolution"]
				_, audio := body["audio"]
				if resolution != explicit || audio != explicit {
					t.Fatalf("optional fields %+v", body)
				}
			}
			q := payloads[1]
			_, neg := q["negative_prompt"]
			_, consent := q["consents"]
			if neg != explicit || consent != explicit {
				t.Fatalf("queue %+v", q)
			}
			if explicit {
				if q["audio"] != false || q["negative_prompt"] != "" {
					t.Fatal(q)
				}
				refs := q["reference_image_urls"].([]any)
				if refs[0] != "http://example.org/ref" || !strings.HasPrefix(refs[1].(string), "data:image/png;base64,") {
					t.Fatal(refs)
				}
			}
		})
	}
}
func TestVideoRetrieveRejectsInvalidAndStopsFailure(t *testing.T) {
	for _, body := range []string{`{}`, `{"error":"bad"}`, `{"status":"FAILED"}`, `{"status":1}`, `<html>no</html>`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			p := NewVeniceProvider("test")
			p.baseURL = s.URL
			var dst bytes.Buffer
			if _, err := p.RetrieveTo(context.Background(), QueuedJob{"m", "q"}, &dst, false); err == nil {
				t.Fatal("accepted invalid status/media")
			}
		})
	}
}
func TestVideoStreamLimitAndNoDelete(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["delete_media_on_completion"] != false {
			t.Error("unsafe deletion")
		}
		w.Header().Set("Content-Type", "video/mp4")
		fmt.Fprint(w, "123456789")
	}))
	defer s.Close()
	p := NewVeniceProvider("test")
	p.baseURL = s.URL
	var dst bytes.Buffer
	if _, err := p.retrieveTo(context.Background(), QueuedJob{"m", "q"}, &dst, false, 4); err == nil {
		t.Fatal("accepted oversized video")
	}
	if _, err := p.Retrieve(context.Background(), QueuedJob{"m", "q"}, true, false); err == nil {
		t.Fatal("accepted unsafe delete-on-read")
	}
}
func TestVideoRefusesRedirectWithoutBodyReplay(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	p := NewVeniceProvider("test")
	p.baseURL = s.URL
	if _, err := p.Queue(context.Background(), Request{Model: "m", Prompt: "secret", Duration: "5s"}); err == nil {
		t.Fatal("redirect accepted")
	}
	if hits != 0 {
		t.Fatal("body replayed")
	}
}

func TestPositiveFractionalDurationSyntax(t *testing.T) {
	for _, v := range []string{"0.5s", "0.01s", "1.5s", "4s", "6s", "8s"} {
		if err := ValidateDuration(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, v := range []string{"0s", "0.0s", "-0.5s", "1e2s", "NaNs", "1.s"} {
		if err := ValidateDuration(v); err == nil {
			t.Errorf("accepted %s", v)
		}
	}
}
