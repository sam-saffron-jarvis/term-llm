package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestScopedGeminiLiveUsesHeaderNotQueryCredential(t *testing.T) {
	checked := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("x-goog-api-key") != "scoped-live-token" || r.Header.Get("X-Term-LLM-Proxy-App") != "term-llm" {
			t.Errorf("scoped credential/attribution handshake was wrong")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		if err = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}}); err != nil {
			return
		}
		checked <- true
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	root := server.URL + "/gemini-live/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Token: "scoped-live-token"}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.LiveConfig{Provider: config.LiveProviderGemini}
	cfg.Gemini.APIKey = "scoped-live-token"
	cfg.Gemini.BaseURL = root
	session, err := NewGeminiProvider(cfg).Start(context.Background(), "", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-checked
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
