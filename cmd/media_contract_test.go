package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// These fixtures intentionally name synthetic models: no live-provider evidence.
func mediaConfig(t *testing.T, root string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("VENICE_API_KEY", "")
	path := filepath.Join(dir, "term-llm", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("default_provider: venice\nproviders:\n  venice:\n    api_key: inference-must-not-be-used\n    base_url: http://127.0.0.1:1/not-used\nimage:\n  provider: venice\n  venice:\n    api_key: test-only-media\n    base_url: %s/relay/v1\n    edit_model: configured-editor\n", root)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func resetMediaFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	type saved struct {
		f       *pflag.Flag
		value   string
		slice   []string
		changed bool
	}
	var old []saved
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		s := saved{f: f, value: f.Value.String(), changed: f.Changed}
		if v, ok := f.Value.(pflag.SliceValue); ok {
			s.slice = v.GetSlice()
			v.Replace(nil)
		} else {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
		}
		f.Changed = false
		old = append(old, s)
	})
	oldOut, oldErr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	t.Cleanup(func() {
		for _, s := range old {
			if v, ok := s.f.Value.(pflag.SliceValue); ok {
				v.Replace(s.slice)
			} else {
				s.f.Value.Set(s.value)
			}
			s.f.Changed = s.changed
		}
		cmd.SetOut(oldOut)
		cmd.SetErr(oldErr)
	})
}
func runMediaCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	if err := cmd.ParseFlags(args); err != nil {
		return "", "", err
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	return out.String(), stderr.String(), err
}
func TestImageCLIWireContracts(t *testing.T) {
	for _, kind := range []string{"generate", "upscale", "background-remove", "url-edit", "mixed-edit", "native-edit"} {
		t.Run(kind, func(t *testing.T) {
			resetMediaFlags(t, imageCmd)
			calls := 0
			png := []byte("\x89PNG\r\n\x1a\nsynthetic")
			local := filepath.Join(t.TempDir(), "input.png")
			os.WriteFile(local, png, 0600)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer test-only-media" {
					t.Error("wrong media authority")
				}
				if strings.Contains(r.URL.Path, "models") {
					t.Fatal("implicit catalog")
				}
				switch kind {
				case "generate":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					for k, v := range map[string]any{"width": float64(640), "height": float64(480), "steps": float64(7), "cfg_scale": float64(0), "negative_prompt": "", "safe_mode": true} {
						if body[k] != v {
							t.Errorf("%s=%v want %v", k, body[k], v)
						}
					}
					if _, ok := body["resolution"]; ok {
						t.Error("inherited resolution with pixel dimensions")
					}
					fmt.Fprintf(w, `{"images":[%q]}`, base64.StdEncoding.EncodeToString(png))
					return
				case "upscale", "background-remove":
					if r.URL.Path != "/relay/v1/image/"+kind {
						t.Error(r.URL.Path)
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal(err)
					}
					defer r.MultipartForm.RemoveAll()
					if kind == "upscale" {
						for k, v := range map[string]string{"scale": "2", "enhance": "false", "enhancePrompt": "detail", "enhanceCreativity": "0", "replication": "0"} {
							if r.FormValue(k) != v {
								t.Errorf("%s=%q", k, r.FormValue(k))
							}
						}
					}
					f, _, err := r.FormFile("image")
					if err != nil {
						t.Error(err)
					} else {
						f.Close()
					}
				default:
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["modelId"] != "explicit-editor" {
						t.Errorf("editor=%v", body["modelId"])
					}
					if kind == "native-edit" {
						if _, present := body["resolution"]; present {
							t.Error("native edit sent resolution", body)
						}
					}
					if kind == "url-edit" || kind == "native-edit" {
						if body["image"] != "https://example.org/ref.png?sig=private" || r.URL.Path != "/relay/v1/image/edit" {
							t.Error(body)
						}
					} else {
						refs := body["images"].([]any)
						if refs[0] != base64.StdEncoding.EncodeToString(png) || refs[1] != "https://example.org/ref.png?sig=private" {
							t.Error(refs)
						}
					}
				}
				w.Write(png)
			}))
			defer s.Close()
			mediaConfig(t, s.URL)
			args := []string{"--no-spinner", "--output", "-", "--timeout", "10m"}
			switch kind {
			case "generate":
				args = append(args, "hello", "--width=640", "--height=480", "--steps=7", "--cfg-scale=0", "--negative-prompt=", "--safe-mode")
			case "upscale":
				args = append(args, "--operation=upscale", "-i", local, "--scale=2", "--enhance=false", "--enhance-prompt=detail", "--enhance-creativity=0", "--replication=0")
			case "background-remove":
				args = append(args, "--operation=background-remove", "-i", local)
			case "url-edit", "native-edit":
				args = append(args, "edit", "-i", "https://example.org/ref.png?sig=private", "--edit-model=explicit-editor")
			case "mixed-edit":
				args = append(args, "edit", "-i", local, "-i", "https://example.org/ref.png?sig=private", "--edit-model=explicit-editor")
			}
			if kind == "native-edit" {
				args = append(args, "--size=native")
			}
			out, _, err := runMediaCommand(t, imageCmd, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal([]byte(out), png) || calls != 1 {
				t.Fatalf("calls %d output %q", calls, out)
			}
		})
	}
}
func TestVideoCLIQuoteQueueRetrieve(t *testing.T) {
	for _, kind := range []string{"quote-only", "queue", "retrieve"} {
		t.Run(kind, func(t *testing.T) {
			resetMediaFlags(t, videoCmd)
			var paths []string
			local := filepath.Join(t.TempDir(), "input.png")
			os.WriteFile(local, []byte("local"), 0600)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer test-only-media" {
					t.Error("wrong authority")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch kind {
				case "quote-only":
					if r.URL.Path != "/relay/v1/video/quote" || body["duration"] != "5s" {
						t.Error(body, r.URL.Path)
					}
					for _, k := range []string{"resolution", "audio", "negative_prompt", "consents"} {
						if _, ok := body[k]; ok {
							t.Error("unexpected", k)
						}
					}
					io.WriteString(w, `{"quote":1}`)
				case "queue":
					if r.URL.Path != "/relay/v1/video/queue" {
						t.Error(r.URL.Path)
					}
					if body["resolution"] != "360p" || body["duration"] != "6s" || body["audio"] != false || body["negative_prompt"] != "" || body["image_url"] != "https://example.org/main.png" {
						t.Error(body)
					}
					consents := body["consents"].(map[string]any)["seedance"].(map[string]any)
					if len(consents) != 3 {
						t.Error(consents)
					}
					for _, v := range consents {
						if v != true {
							t.Error(consents)
						}
					}
					refs := body["reference_image_urls"].([]any)
					if refs[0] != "https://example.org/ref.png" || !strings.HasPrefix(refs[1].(string), "data:image/png;base64,") {
						t.Error(refs)
					}
					io.WriteString(w, `{"model":"synthetic","queue_id":"job-123"}`)
				case "retrieve":
					if r.URL.Path != "/relay/v1/video/retrieve" || body["delete_media_on_completion"] != false {
						t.Error(r.URL.Path, body)
					}
					io.WriteString(w, `{"status":"NEW_UNKNOWN_STATUS"}`)
				}
			}))
			defer s.Close()
			mediaConfig(t, s.URL)
			args := []string{"--json", "--model=synthetic", "--output", filepath.Join(t.TempDir(), "video.mp4")}
			switch kind {
			case "quote-only":
				args = append(args, "hello", "--quote-only", "--input=/not/read/for/quote")
			case "queue":
				args = append(args, "hello", "--skip-quote", "--no-wait", "--duration=6s", "--resolution=360p", "--negative-prompt=", "--audio=false", "--seedance-consent", "-i", "https://example.org/main.png", "-r", "https://example.org/ref.png", "-r", local)
			case "retrieve":
				args = append(args, "--queue-id=job-123", "--no-wait")
			}
			out, stderr, err := runMediaCommand(t, videoCmd, args...)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(out, err)
			}
			if len(paths) != 1 {
				t.Error(paths)
			}
			if kind == "queue" && !strings.Contains(stderr, "model=synthetic queue_id=job-123") {
				t.Error("missing JSON-mode receipt", stderr)
			}
			if kind != "quote-only" && (result["job"] == nil || result["recovery"] == nil) {
				t.Error(result)
			}
			if kind == "quote-only" && result["resolution"] != nil {
				t.Error("invented resolution", result)
			}
		})
	}
}
func TestVideoRetrieveRejectsExplicitGenerationFlags(t *testing.T) {
	for _, flag := range []string{"--audio=false", "--negative-prompt=", "--input=/missing", "--skip-quote=false", "--seedance-consent=false"} {
		t.Run(flag, func(t *testing.T) {
			resetMediaFlags(t, videoCmd)
			_, _, err := runMediaCommand(t, videoCmd, "--queue-id=job", "--model=m", flag)
			if err == nil || !strings.Contains(err.Error(), "generation options") {
				t.Fatal(err)
			}
		})
	}
}
func TestTypedMediaCatalogCLI(t *testing.T) {
	for _, kind := range []string{"image", "inpaint", "video"} {
		t.Run(kind, func(t *testing.T) {
			resetMediaFlags(t, modelsCmd)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/relay/v1/models" || r.URL.RawQuery != "type="+kind || r.Header.Get("Authorization") != "Bearer test-only-media" {
					t.Error(r.URL, r.Header)
				}
				fmt.Fprintf(w, `{"data":[{"id":"synthetic","type":%q,"model_spec":{"name":"Name","privacy":"private","constraints":{"model_type":"kind","durations":["6s"],"resolutions":["4k"]}}}]}`, kind)
			}))
			defer s.Close()
			mediaConfig(t, s.URL)
			out, _, err := runMediaCommand(t, modelsCmd, "--provider=venice", "--type="+kind, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var models []map[string]any
			if err := json.Unmarshal([]byte(out), &models); err != nil || len(models) != 1 || models[0]["type"] != kind {
				t.Fatal(out, err)
			}
		})
	}
}

func TestVideoCLIRetrieveErrorRetainsJobOneJSON(t *testing.T) {
	resetMediaFlags(t, videoCmd)
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/relay/v1/video/retrieve" {
			t.Error("retrieve mode called", r.URL.Path)
		}
		http.Error(w, "synthetic retrieve failure", 500)
	}))
	defer s.Close()
	mediaConfig(t, s.URL)
	out, _, err := runMediaCommand(t, videoCmd, "--model=synthetic", "--queue-id=job-123", "--json", "--output", filepath.Join(t.TempDir(), "video.mp4"))
	if err == nil {
		t.Fatal("missing error")
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(out, err)
	}
	if result["status"] != "retrieve_failed" || result["model"] != "synthetic" || result["job"] == nil || !strings.Contains(result["recovery"].(string), "--queue-id 'job-123'") || calls != 1 {
		t.Fatal(result, calls)
	}
	if err := decoder.Decode(&result); err != io.EOF {
		t.Fatal("not one JSON document", err)
	}
}

func TestVideoCLIAtomicSaveAndCleanupWire(t *testing.T) {
	for _, mode := range []string{"keep", "cleanup", "save-failure"} {
		t.Run(mode, func(t *testing.T) {
			resetMediaFlags(t, videoCmd)
			output := filepath.Join(t.TempDir(), "video.mp4")
			var paths []string
			videoBytes := []byte("\x00\x00\x00\x18ftypmp42synthetic")
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/relay/v1/video/queue":
					io.WriteString(w, `{"model":"synthetic","queue_id":"job-123"}`)
				case "/relay/v1/video/retrieve":
					if body["delete_media_on_completion"] != false {
						t.Error("unsafe deletion")
					}
					if mode == "save-failure" {
						if err := os.Mkdir(output, 0700); err != nil {
							t.Error(err)
						}
					}
					w.Header().Set("Content-Type", "video/mp4")
					w.Write(videoBytes)
				case "/relay/v1/video/complete":
					if mode != "cleanup" {
						t.Error("unexpected cleanup")
					}
					if body["model"] != "synthetic" || body["queue_id"] != "job-123" {
						t.Error(body)
					}
					data, err := os.ReadFile(output)
					if err != nil || !bytes.Equal(data, videoBytes) {
						t.Error("cleanup before durable local save", err)
					}
					io.WriteString(w, `{"success":true}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
				}
			}))
			defer s.Close()
			mediaConfig(t, s.URL)
			args := []string{"hello", "--model=synthetic", "--skip-quote", "--json", "--output", output}
			if mode != "keep" {
				args = append(args, "--delete-remote")
			}
			out, _, err := runMediaCommand(t, videoCmd, args...)
			if mode == "save-failure" {
				if err == nil || len(paths) != 2 || !strings.Contains(out, `"save_failed"`) {
					t.Fatal(out, err, paths)
				}
			} else {
				want := 2
				if mode == "cleanup" {
					want = 3
				}
				if err != nil || len(paths) != want || !strings.Contains(out, `"completed"`) {
					t.Fatal(out, err, paths)
				}
			}
		})
	}
}

func TestVideoCLIInvalidBeforePreflightOrSpend(t *testing.T) {
	resetMediaFlags(t, videoCmd)
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("invalid request sent", r.URL.Path) }))
	defer s.Close()
	mediaConfig(t, s.URL)
	output := filepath.Join(t.TempDir(), "not-created", "video.mp4")
	out, _, err := runMediaCommand(t, videoCmd, "hello", "--model=synthetic", "--skip-quote", "--duration=5", "--json", "--output", output)
	if err == nil || !strings.Contains(out, `"status": "invalid"`) || strings.Contains(out, "do not requeue") || calls != 0 {
		t.Fatal(out, err, calls)
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("local preflight before validation", err)
	}
}
