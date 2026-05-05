package main

import "time"

type webChat1000Task struct{}

func (webChat1000Task) Name() string       { return "go_web_chat_1000" }
func (webChat1000Task) Language() string   { return "go" }
func (webChat1000Task) Difficulty() string { return "hard-web-concurrency" }
func (webChat1000Task) Prompt() string {
	return `Write a complete Go source file for package main, including any imports, that defines exactly this function:

func NewChatServer() http.Handler

Implement a tiny in-memory multi-room web chat API using only the Go standard library.

Required endpoints:

POST /rooms/{room}/messages
- Request body JSON: {"user":"alice","text":"hello"}
- user and text must be non-empty strings
- Append the message to that room and assign a monotonically increasing seq number starting at 1 per room
- Return HTTP 201 with the stored message as JSON: {"seq":1,"user":"alice","text":"hello"}

GET /rooms/{room}/messages
- Return HTTP 200 with a JSON array of all stored messages for that room in seq order
- Unknown rooms return an empty JSON array []

Anything else should return an appropriate 4xx status.

The handler must be safe for 1000 concurrent users posting at the same time. Do not include a main function.`
}

func (webChat1000Task) Score(response string, timeout time.Duration) ScoreResult {
	return scoreGoFunctionWithRace(response, timeout, `
type chatMessage struct {
	Seq  int    `+"`"+`json:"seq"`+"`"+`
	User string `+"`"+`json:"user"`+"`"+`
	Text string `+"`"+`json:"text"`+"`"+`
}

func TestGenerated(t *testing.T) {
	h := NewChatServer()

	// Basic validation first: bad JSON and empty fields should not be accepted.
	badReq := httptest.NewRequest(http.MethodPost, "/rooms/lobby/messages", strings.NewReader(`+"`"+`{"user":"","text":"hello"}`+"`"+`))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code < 400 || badRec.Code > 499 {
		t.Fatalf("empty user status = %d, want 4xx", badRec.Code)
	}

	const users = 1000
	var wg sync.WaitGroup
	for i := 0; i < users; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`+"`"+`{"user":"user-%d","text":"hello-%d"}`+"`"+`, i, i)
			req := httptest.NewRequest(http.MethodPost, "/rooms/lobby/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Errorf("POST status = %d body=%s", rec.Code, rec.Body.String())
				return
			}
			var msg chatMessage
			if err := json.NewDecoder(rec.Body).Decode(&msg); err != nil {
				t.Errorf("decode POST response: %v", err)
				return
			}
			if msg.Seq < 1 || msg.Seq > users || msg.User != fmt.Sprintf("user-%d", i) || msg.Text != fmt.Sprintf("hello-%d", i) {
				t.Errorf("bad stored message: %#v", msg)
			}
		}()
	}
	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/rooms/lobby/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", rec.Code, rec.Body.String())
	}
	var messages []chatMessage
	if err := json.NewDecoder(rec.Body).Decode(&messages); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if len(messages) != users {
		t.Fatalf("message count = %d, want %d", len(messages), users)
	}
	seenSeq := make(map[int]bool, users)
	seenUsers := make(map[string]bool, users)
	lastSeq := 0
	for _, msg := range messages {
		if msg.Seq <= lastSeq {
			t.Fatalf("messages not in seq order around seq %d after %d", msg.Seq, lastSeq)
		}
		lastSeq = msg.Seq
		if msg.Seq < 1 || msg.Seq > users || seenSeq[msg.Seq] {
			t.Fatalf("bad or duplicate seq: %d", msg.Seq)
		}
		seenSeq[msg.Seq] = true
		seenUsers[msg.User] = true
	}
	for i := 0; i < users; i++ {
		if !seenUsers[fmt.Sprintf("user-%d", i)] {
			t.Fatalf("missing user-%d", i)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/rooms/empty/messages", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty room = status %d body %q, want 200 []", rec.Code, rec.Body.String())
	}
}
`, "encoding/json", "fmt", "net/http", "net/http/httptest", "strings", "sync")
}
