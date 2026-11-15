package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramConfiguredBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/nested/botfake-token/sendMessage" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var body telegramSendRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != 42 || body.Text != "hello" {
			t.Errorf("body=%+v", body)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	t.Setenv("TELEGRAM_BASE_URL", server.URL+"/proxy/nested/")
	if err := SendTelegramMessage(context.Background(), "fake-token", 42, "hello", "Markdown"); err != nil {
		t.Fatal(err)
	}
}
