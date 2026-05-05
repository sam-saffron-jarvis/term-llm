package main

import (
	"strings"
	"testing"
	"time"
)

func TestExtractCodeFencedGoBlock(t *testing.T) {
	code, err := extractCode("prose\n```go\npackage main\nfunc X() {}\n```\nmore")
	if err != nil {
		t.Fatalf("extractCode failed: %v", err)
	}
	if !strings.Contains(code, "func X") {
		t.Fatalf("unexpected code: %q", code)
	}
}

func TestScoreGoFunctionPasses(t *testing.T) {
	result := scoreGoFunction(`package main

func BinarySearch(xs []int, target int) int {
	lo, hi := 0, len(xs)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if xs[mid] == target { return mid }
		if xs[mid] < target { lo = mid + 1 } else { hi = mid - 1 }
	}
	return -1
}`, 10*time.Second, `
func TestGenerated(t *testing.T) {
	if got := BinarySearch([]int{1, 3, 5}, 3); got != 1 {
		t.Fatalf("got %d", got)
	}
}
`)
	if !result.Pass || result.Score != 1 {
		t.Fatalf("expected pass, got %#v", result)
	}
}

func TestWebChat1000TaskReferenceImplementationPasses(t *testing.T) {
	code := `package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type storedMessage struct {
	Seq  int    ` + "`" + `json:"seq"` + "`" + `
	User string ` + "`" + `json:"user"` + "`" + `
	Text string ` + "`" + `json:"text"` + "`" + `
}

type chatServer struct {
	mu    sync.Mutex
	rooms map[string][]storedMessage
}

func NewChatServer() http.Handler {
	return &chatServer{rooms: make(map[string][]storedMessage)}
}

func (s *chatServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "rooms" || parts[2] != "messages" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}
	room := parts[1]
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPost:
		var in struct { User, Text string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.User == "" || in.Text == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		msg := storedMessage{Seq: len(s.rooms[room]) + 1, User: in.User, Text: in.Text}
		s.rooms[room] = append(s.rooms[room], msg)
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(msg)
	case http.MethodGet:
		s.mu.Lock()
		messages := append([]storedMessage(nil), s.rooms[room]...)
		s.mu.Unlock()
		if messages == nil { messages = []storedMessage{} }
		_ = json.NewEncoder(w).Encode(messages)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
`
	result := webChat1000Task{}.Score(code, 20*time.Second)
	if !result.Pass || result.Score != 1 {
		t.Fatalf("expected pass, detail=%s stdout=%s stderr=%s", result.Details, result.Stdout, result.Stderr)
	}
}
