package serve

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramConfiguredAPIAndFileRoots(t *testing.T) {
	called := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called[r.URL.Path]++
		switch r.URL.Path {
		case "/proxy/nested/botfake-token/getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"testbot"}}`)
		case "/proxy/nested/file/botfake-token/test.ogg":
			fmt.Fprint(w, "audio")
		default:
			t.Errorf("path=%s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TELEGRAM_BASE_URL", server.URL+"/proxy/nested/")
	if _, err := newTelegramBotAPI("fake-token"); err != nil {
		t.Fatal(err)
	}
	resp, err := downloadTelegramFile("https://api.telegram.org/file/botfake-token/test.ogg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "audio" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if len(called) != 2 {
		t.Fatalf("calls=%v", called)
	}
}
