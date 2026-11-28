package requestmeta

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestReviewExactGeminiAndEscapedBedrockPaths(t *testing.T) {
	for _, path := range []string{"/models/gemini-2.5:preview:generateContent", "/v1/models/gemini-2.5%3Apreview:generateContent", "/v1beta/models/gemini-2.5:preview:streamGenerateContent"} {
		o := New(io.NopCloser(strings.NewReader(`{"generationConfig":{"maxOutputTokens":7}}`)), context.Background(), "POST", path, path, "friendly-unrelated-label", "", http.Header{"Content-Type": {"application/json"}}, -1)
		_, _ = io.Copy(io.Discard, o)
		m := o.Finalize()
		if m.State != "observed" || m.Model != "gemini-2.5:preview" || m.OutputLimit == nil || *m.OutputLimit != 7 {
			t.Fatal(path, m)
		}
	}
	for _, path := range []string{"/arbitrary/v1/models/model:generateContent", "/models/a/b:generateContent", "/models/a%2Fb:generateContent", "/v1beta/models/a:generateContent/other", "/model/anthropic.test/invoke/extra", "/model/anthropic.test/invoke%2Fextra", "/model/arn:profile/id/invoke"} {
		if got := protocolFor("POST", path, path); got != "" {
			t.Fatal(path, got)
		}
	}
	model := "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/abc123"
	endpoint := "/model/" + url.PathEscape(model) + "/invoke-with-response-stream"
	for _, body := range []string{`{"max_tokens":7}`, `{"anthropic_version":"other","max_tokens":7}`, `{"anthropic_version":null,"max_tokens":7}`, `{"anthropic_version":"bedrock-2023-05-31","max_tokens":7}`} {
		req := httptest.NewRequest("POST", "/prefix"+endpoint, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		target, _ := url.Parse("https://fixture.invalid/prefix")
		o := ForRequest(req.Body, req, target, strings.TrimPrefix(endpoint, "/"), "not-anthropic-label", target.String())
		got, err := io.ReadAll(o)
		if err != nil || string(got) != body {
			t.Fatal("bytes changed", err)
		}
		m := o.Finalize()
		want := "unsupported"
		if strings.Contains(body, `"bedrock-2023-05-31"`) {
			want = "observed"
		}
		if m.State != want {
			t.Fatal(m)
		}
		if want == "observed" && (m.Model != model || m.OutputLimit == nil || *m.OutputLimit != 7) {
			t.Fatal(m)
		}
		if want == "unsupported" && (m.Model != "" || m.OutputLimit != nil) {
			t.Fatal("non-Anthropic settings inferred", m)
		}
	}
}

func TestReviewProtocolScopedValidationAndNullPresence(t *testing.T) {
	for _, tt := range []struct{ protocol, body string }{
		{"chat_completions", `{"model":"test","text":"PRIVATE","thinking":null,"options":"extension","reasoning":{"effort":null},"chat_template_kwargs":false}`},
		{"messages", `{"model":"test","text":"PRIVATE","reasoning":false,"options":null,"output_config":{"effort":"high"}}`},
		{"gemini", `{"text":"PRIVATE","options":null,"reasoning":null,"generationConfig":{"maxOutputTokens":5}}`},
		{"ollama", `{"model":"test","text":"PRIVATE","reasoning":null,"thinking":false,"options":{"num_predict":5}}`},
	} {
		m := parse([]byte(tt.body), tt.protocol, "", "", "")
		if m.State != "observed" {
			t.Fatal(tt.protocol, m)
		}
	}
	for _, body := range []string{`{"tool_choice":"auto","max_tokens":5}`, `{"tool_choice":false,"max_tokens":5}`, `{"tool_choice":null,"max_tokens":5}`} {
		m := parse([]byte(body), "messages", "", "", "")
		if m.State != "invalid" || m.OutputLimit == nil || *m.OutputLimit != 5 || m.ToolChoice != "" {
			t.Fatal(m)
		}
	}
	for _, body := range []string{`{"temperature":null,"model":"test"}`, `{"reasoning":null,"model":"test"}`, `{"tools":null,"model":"test"}`} {
		m := parse([]byte(body), "responses", "", "", "")
		if m.State != "invalid" || m.Model != "test" {
			t.Fatal("null silently became not sent", m)
		}
	}
	m := parse([]byte(`{"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"PRIVATE"}]}}`), "responses", "", "", "")
	if m.State != "observed" || m.ToolChoice != "allowed_tools" {
		t.Fatal(m)
	}
	m = parse([]byte(`{"tool_choice":{"type":"allowed_tools"}}`), "chat_completions", "", "", "")
	if m.State != "invalid" || m.ToolChoice != "" {
		t.Fatal(m)
	}
}
