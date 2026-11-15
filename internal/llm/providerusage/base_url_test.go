package providerusage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/credentials"
)

func TestChatGPTDedicatedUsageRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := credentials.SaveChatGPTCredentials(&credentials.ChatGPTCredentials{AccessToken: "fake", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/account/nested/wham/usage" || r.Header.Get("Authorization") != "Bearer fake" {
			t.Errorf("request=%s", r.URL)
		}
		fmt.Fprint(w, `{"plan_type":"pro"}`)
	}))
	defer server.Close()
	t.Setenv("CHATGPT_USAGE_BASE_URL", server.URL+"/account/nested/")
	t.Setenv("OPENAI_BASE_URL", "http://not-used.invalid")
	report, err := FetchWithBaseURL(context.Background(), "chatgpt", "", "http://codex-not-used.invalid")
	if err != nil || !called || report.Plan != "pro" {
		t.Fatalf("report=%v err=%v called=%v", report, err, called)
	}
}
