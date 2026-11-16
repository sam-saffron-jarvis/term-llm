package clibin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// These variables opt in to running a REAL vendor executable against a
// loopback-only synthetic gateway. They are never set by the normal suite:
//
//	TERM_LLM_CLI_PROXY_EXEC_GROK    absolute path of the real grok executable
//	TERM_LLM_CLI_PROXY_EXEC_CURSOR  absolute path of the real cursor-agent launcher
//	TERM_LLM_CLI_PROXY_EXEC_VIBE    absolute path of the real vibe-acp executable
//	TERM_LLM_CLI_PROXY_EXEC_JAIL    absolute path of an egress-denying launcher
//	                                invoked as: JAIL <log> -- <command> <args...>
//
// The launcher must refuse and log ("DENY ...") every non-loopback connection;
// without it there is no proof that the CLI stayed on the scoped route.
const (
	cliProxyExecGrokEnv   = "TERM_LLM_CLI_PROXY_EXEC_GROK"
	cliProxyExecCursorEnv = "TERM_LLM_CLI_PROXY_EXEC_CURSOR"
	cliProxyExecVibeEnv   = "TERM_LLM_CLI_PROXY_EXEC_VIBE"
	cliProxyExecJailEnv   = "TERM_LLM_CLI_PROXY_EXEC_JAIL"
)

type cliProxyExecRequest struct {
	Method, Path, Model string
	AtMillis            int64
	Scoped              bool
}

// cliProxyExecGateway is a synthetic OpenAI-shaped gateway. It never contacts
// an upstream and answers only requests that carry the scoped bearer.
type cliProxyExecGateway struct {
	mu       sync.Mutex
	requests []cliProxyExecRequest
	leaked   []string
	canaries []string
	// Cursor only: closed once a BidiAppend carrying promptHex arrived.
	toolNames, toolResults []string // Grok only.
	promptHex              string
	promptSeen             chan struct{}
	promptOnce             sync.Once
}

func (g *cliProxyExecGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &payload)
	scoped := r.Header.Get("Authorization") == "Bearer "+cliProxyTestToken
	if dump := os.Getenv("TERM_LLM_CLI_PROXY_EXEC_DUMP"); dump != "" && r.Method == http.MethodPost {
		if f, err := os.OpenFile(dump, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s %s\n%s\n\n", r.Method, r.URL.Path, body)
			f.Close()
		}
	}
	g.mu.Lock()
	g.requests = append(g.requests, cliProxyExecRequest{Method: r.Method, Path: r.URL.Path, Model: payload.Model, AtMillis: time.Now().UnixMilli(), Scoped: scoped})
	for _, canary := range g.canaries {
		if strings.Contains(string(body), canary) || strings.Contains(fmt.Sprint(r.Header), canary) {
			g.leaked = append(g.leaked, r.URL.Path)
		}
	}
	g.mu.Unlock()
	if !scoped {
		http.Error(w, `{"error":{"message":"fixture: missing scoped bearer"}}`, http.StatusUnauthorized)
		return
	}
	switch {
	case strings.Contains(r.URL.Path, "/cursor-bin"):
		g.serveCursor(w, r, body)
	case strings.HasSuffix(r.URL.Path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"grok-fixture","object":"model","owned_by":"fixture"}]}`)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		w.Header().Set("Content-Type", "text/event-stream")
		call := ""
		if strings.HasPrefix(r.URL.Path, "/vibe-bin/") {
			call = g.directToolCall(body)
		} else {
			call = g.grokToolCall(body)
		}
		if call != "" {
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[%s]},\"finish_reason\":null}]}\n\n", payload.Model, call)
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n", payload.Model)
			return
		}
		fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"FIXTURE-OK\"},\"finish_reason\":null}]}\n\n", payload.Model)
		fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n", payload.Model)
		io.WriteString(w, "data: [DONE]\n\n")
	case strings.HasSuffix(r.URL.Path, "/responses"):
		message := `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"FIXTURE-OK","annotations":[]}]}`
		done := fmt.Sprintf(`{"id":"resp_1","object":"response","status":"completed","model":%q,"output":[%s],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`, payload.Model, message)
		if !payload.Stream {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, done)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range [][2]string{
			{"response.created", `"response":{"id":"resp_1","object":"response","status":"in_progress","output":[]}`},
			{"response.output_item.added", `"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}`},
			{"response.output_text.delta", `"item_id":"msg_1","output_index":0,"content_index":0,"delta":"FIXTURE-OK"`},
			{"response.output_item.done", `"output_index":0,"item":` + message},
			{"response.completed", `"response":` + done},
		} {
			fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q,%s}\n\n", event[0], event[0], event[1])
		}
	default:
		http.Error(w, `{"error":{"message":"fixture: no such route"}}`, http.StatusNotFound)
	}
}

var grokMCPToolName = regexp.MustCompile(`[A-Za-z0-9_.:-]*read_file`)

// grokToolCall scripts one MCP round trip through Grok's only exposed tools:
// search_tool to discover the bridged tool, then use_tool to call it. It
// returns "" for requests without tools and once the tool result came back.
func (g *cliProxyExecGateway) grokToolCall(body []byte) string {
	var request struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Tools) == 0 {
		return ""
	}
	var results []string
	for _, message := range request.Messages {
		if message.Role == "tool" {
			results = append(results, string(message.Content))
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.toolNames = g.toolNames[:0]
	for _, tool := range request.Tools {
		var spec struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		_ = json.Unmarshal(tool, &spec)
		g.toolNames = append(g.toolNames, spec.Function.Name)
	}
	call := func(name string, args any) string {
		encoded, _ := json.Marshal(args)
		return fmt.Sprintf(`{"index":0,"id":"call_%d","type":"function","function":{"name":%q,"arguments":%q}}`, len(results), name, encoded)
	}
	switch len(results) {
	case 0:
		return call("search_tool", map[string]any{"query": "term-llm read_file"})
	case 1:
		name := grokMCPToolName.FindString(results[0])
		g.toolResults = append(g.toolResults, "search: "+results[0])
		return call("use_tool", map[string]any{"tool_name": name, "tool_input": map[string]string{"path": "fixture.txt"}})
	default:
		g.toolResults = append(g.toolResults, results[1])
		return ""
	}
}

// directToolCall scripts one round trip for a CLI that exposes bridged MCP
// tools to the model as ordinary functions (Vibe).
func (g *cliProxyExecGateway) directToolCall(body []byte) string {
	var request struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Tools) == 0 {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.toolNames = g.toolNames[:0]
	target := ""
	for _, tool := range request.Tools {
		g.toolNames = append(g.toolNames, tool.Function.Name)
		if strings.HasSuffix(tool.Function.Name, "read_file") {
			target = tool.Function.Name
		}
	}
	for _, message := range request.Messages {
		if message.Role == "tool" {
			g.toolResults = append(g.toolResults, string(message.Content))
			return ""
		}
	}
	if target == "" {
		return ""
	}
	return fmt.Sprintf(`{"index":0,"id":"call_0","type":"function","function":{"name":%q,"arguments":"{\"path\":\"fixture.txt\"}"}}`, target)
}

// serveCursor answers Cursor's Connect protocol: empty unary messages, a
// one-model catalog, and one framed text answer on the HTTP/1 agent stream.
func (g *cliProxyExecGateway) serveCursor(w http.ResponseWriter, r *http.Request, body []byte) {
	contentType := r.Header.Get("Content-Type")
	field := func(tag byte, value string) string { return string([]byte{tag, byte(len(value))}) + value }
	switch {
	case strings.HasSuffix(r.URL.Path, "/agent.v1.AgentService/RunSSE"):
		// Answering before the prompt-bearing append would prove nothing.
		select {
		case <-g.promptSeen:
		case <-time.After(30 * time.Second):
			http.Error(w, `{"code":"deadline_exceeded"}`, http.StatusGatewayTimeout)
			return
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		// A source-derived terminal checkpoint is an opt-in protocol probe,
		// not evidence of resumable state or a bridged Cursor tool round trip.
		checkpoint := ""
		if os.Getenv(cliProxyExecCursorEnv+"_TERMINAL_CHECKPOINT") != "" {
			checkpoint = "\x00\x00\x00\x00\x02\x1a\x00"
		}
		w.Write([]byte("\x00\x00\x00\x00\x08\x0a\x06\x0a\x04\x0a\x02OK" + "\x00\x00\x00\x00\x04\x0a\x02\x72\x00" + checkpoint + "\x02\x00\x00\x00\x02{}"))
	case strings.HasSuffix(r.URL.Path, "/aiserver.v1.BidiService/BidiAppend"):
		if strings.Contains(strings.ToLower(string(body)), g.promptHex) {
			g.promptOnce.Do(func() { close(g.promptSeen) })
		}
		w.Header().Set("Content-Type", contentType)
		if strings.Contains(contentType, "json") {
			io.WriteString(w, "{}")
		}
	case strings.Contains(contentType, "json"):
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{}")
	case strings.HasSuffix(r.URL.Path, "/GetUsableModels"), strings.HasSuffix(r.URL.Path, "/GetDefaultModelForCli"):
		details := field(0x0a, "auto") + field(0x1a, "auto") + field(0x22, "Auto") + field(0x2a, "Auto") + field(0x32, "auto")
		w.Header().Set("Content-Type", "application/proto")
		io.WriteString(w, field(0x0a, details))
	default:
		w.Header().Set("Content-Type", "application/proto")
	}
}

// writeCLIProxyExecWrapper puts name on PATH as the real executable run under
// the egress-denying launcher, and returns the launcher's log path.
func writeCLIProxyExecWrapper(t *testing.T, work, name, jail, real string) string {
	t.Helper()
	jailLog := filepath.Join(work, "egress.log")
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %q %q -- %q \"$@\"\n", jail, jailLog, real)
	if err := os.WriteFile(filepath.Join(bin, name), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return jailLog
}

// assertCLIProxyExecContained fails on any refused non-loopback connection.
// allowDNS tolerates refused resolver lookups, which carry no credential.
func assertCLIProxyExecContained(t *testing.T, jailLog string, allowDNS bool) {
	t.Helper()
	egress, _ := os.ReadFile(jailLog)
	if len(egress) == 0 {
		t.Fatal("egress launcher recorded nothing; containment is unproven")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(egress)), "\n") {
		if !strings.HasPrefix(line, "DENY") {
			continue
		}
		if strings.Contains(line, "unix:") || allowDNS && strings.HasSuffix(line, "port=53") {
			t.Logf("refused local lookup: %s", line)
			continue
		}
		t.Errorf("non-loopback egress attempted: %s", line)
	}
}

func TestRealCursorExecutableStaysOnScopedProxyRoutes(t *testing.T) {
	realCursor, jail := os.Getenv(cliProxyExecCursorEnv), os.Getenv(cliProxyExecJailEnv)
	if realCursor == "" || jail == "" {
		t.Skipf("set %s and %s to run the real cursor-agent against a synthetic gateway", cliProxyExecCursorEnv, cliProxyExecJailEnv)
	}
	work := t.TempDir()
	jailLog := writeCLIProxyExecWrapper(t, work, "cursor-agent", jail, realCursor)
	// Poisoned local state: a login and API key the adapter must never use.
	const fileCanary, envCanary = "poisoned-auth-file-canary", "poisoned-env-key-canary"
	fakeHome, fakeXDG := filepath.Join(work, "user-home"), filepath.Join(work, "user-xdg")
	poison := `{"accessToken":"` + fileCanary + `","refreshToken":"` + fileCanary + `","apiKey":"` + fileCanary + `"}`
	for _, dir := range []string{filepath.Join(fakeXDG, "cursor"), filepath.Join(fakeHome, ".config", "cursor"), filepath.Join(fakeHome, ".cursor")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(poison), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", fakeXDG)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(work, "cache"))
	t.Setenv("CURSOR_API_KEY", envCanary)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(fakeHome, ".cursor"))
	// cursor-agent talks to itself over a socket in its temporary directory.
	t.Setenv("NETJAIL_ALLOW_UNIX_PREFIX", os.TempDir()+string(os.PathSeparator))

	const prompt = "Reply with OK."
	gateway := &cliProxyExecGateway{canaries: []string{fileCanary, envCanary}, promptHex: fmt.Sprintf("%x", prompt), promptSeen: make(chan struct{})}
	server := httptest.NewServer(gateway)
	defer server.Close()
	root := server.URL + "/cursor-bin"
	registerCLIProxyBindings(t, "cursor", root, root+"-agent", root+"-repo")

	provider := NewCursorBinProvider("auto-smart", map[string]string{cursorProxyRootEnv: root})
	if os.Getenv(cliProxyExecCursorEnv+"_OMIT_ALLOWED_TOOLS") == "" {
		// Supported releases: the unmodified provider path, tool allowlist and
		// MCP bridge configuration included.
		defer provider.CleanupMCP()
		provider.SetToolExecutor(func(context.Context, string, json.RawMessage) (core.ToolOutput, error) {
			return core.ToolOutput{}, fmt.Errorf("the fixture never requests a tool")
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		stream, err := provider.Stream(ctx, core.Request{
			Messages: []core.Message{core.UserText(prompt)},
			Tools: []core.ToolSpec{{Name: "read_file", Description: "Read a file.", Schema: map[string]interface{}{
				"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}, "required": []string{"path"},
			}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := core.CollectTextStream(stream, nil)
		stream.Close()
		if err != nil || !strings.Contains(result.Text, "OK") {
			egress, _ := os.ReadFile(jailLog)
			t.Fatalf("provider turn through scoped routes: %v text=%q\nrequests: %+v\negress:\n%s", err, result.Text, gateway.requests, egress)
		}
	} else {
		cursorProxyExecLegacyTurn(t, provider, prompt, jailLog, gateway)
	}
	select {
	case <-gateway.promptSeen:
	default:
		t.Error("prompt never reached the scoped agent route")
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	agentStream := false
	for _, request := range gateway.requests {
		t.Logf("gateway: %s %s scoped=%t", request.Method, request.Path, request.Scoped)
		// cursor-agent probes GetMe once before it adopts the token.
		if !request.Scoped && !strings.HasSuffix(request.Path, ".DashboardService/GetMe") {
			t.Errorf("request without the scoped bearer: %s", request.Path)
		}
		switch {
		case strings.HasPrefix(request.Path, "/cursor-bin-agent/"):
			agentStream = agentStream || strings.HasSuffix(request.Path, "/RunSSE")
		case strings.HasPrefix(request.Path, "/cursor-bin-repo/"), strings.HasPrefix(request.Path, "/cursor-bin/"):
		default:
			t.Errorf("request outside the scoped routes: %s", request.Path)
		}
	}
	if !agentStream {
		t.Error("agent stream did not use the HTTP/1 scoped agent route")
	}
	if len(gateway.leaked) > 0 {
		t.Errorf("poisoned local credential reached the gateway: %v", gateway.leaked)
	}
	assertCLIProxyExecContained(t, jailLog, true)
}

func TestRealGrokExecutableStaysOnScopedProxyRoute(t *testing.T) {
	realGrok, jail := os.Getenv(cliProxyExecGrokEnv), os.Getenv(cliProxyExecJailEnv)
	if realGrok == "" || jail == "" {
		t.Skipf("set %s and %s to run the real grok executable against a synthetic gateway", cliProxyExecGrokEnv, cliProxyExecJailEnv)
	}
	work := t.TempDir()
	jailLog := writeCLIProxyExecWrapper(t, work, "grok", jail, realGrok)
	// Poisoned local state: a login and API key the adapter must never use.
	fakeHome := filepath.Join(work, "user-home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".grok"), 0o700); err != nil {
		t.Fatal(err)
	}
	const fileCanary, envCanary = "poisoned-auth-file-canary", "poisoned-env-key-canary"
	poison := `{"access_token":"` + fileCanary + `","refresh_token":"` + fileCanary + `","expires_in":3600}`
	if err := os.WriteFile(filepath.Join(fakeHome, ".grok", "auth.json"), []byte(poison), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(work, "cache"))
	t.Setenv("XAI_API_KEY", envCanary)
	t.Setenv("GROK_CODE_XAI_API_KEY", envCanary)
	t.Setenv(grokLegacyTransportEnv, "")

	gateway := &cliProxyExecGateway{canaries: []string{fileCanary, envCanary}}
	server := httptest.NewServer(gateway)
	defer server.Close()
	root := server.URL + "/grok-bin"
	registerCLIProxyBindings(t, "grok", root)

	provider := NewGrokBinProvider("grok-fixture", map[string]string{grokProxyRootEnv: root})
	provider.SetPreferOAuth(true)
	defer provider.CleanupMCP()

	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("model listing through scoped route: %v", err)
	}
	if len(models) != 1 || models[0].ID != "grok-fixture" {
		t.Fatalf("models = %+v", models)
	}

	const toolMarker = "local-read-file-roundtrip-marker"
	if err := os.WriteFile(filepath.Join(work, "fixture.txt"), []byte(toolMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	var toolCalls []string
	provider.SetToolExecutor(func(context.Context, string, json.RawMessage) (core.ToolOutput, error) {
		return core.ToolOutput{}, fmt.Errorf("the engine, not the provider, executes bridged tools")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stream, err := provider.Stream(ctx, core.Request{
		Messages: []core.Message{core.UserText("Reply with OK.")},
		Tools: []core.ToolSpec{{Name: "read_file", Description: "Read a file.", Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}, "required": []string{"path"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The test stands in for the engine: bridged tool calls are executed locally.
	result, err := core.CollectTextStream(stream, func(event core.Event) error {
		if event.Type == core.EventToolCall && event.ToolResponse != nil && event.Tool != nil {
			toolCalls = append(toolCalls, event.Tool.Name+" "+string(event.Tool.Arguments))
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(event.Tool.Arguments, &args); err != nil || args.Path != "fixture.txt" {
				return fmt.Errorf("unexpected fixture file argument")
			}
			data, err := os.ReadFile(filepath.Join(work, args.Path))
			if err != nil {
				return fmt.Errorf("read owned fixture: %w", err)
			}
			event.ToolResponse <- core.ToolExecutionResponse{Result: core.TextOutput(string(data))}
		}
		return nil
	})
	stream.Close()
	if err != nil {
		t.Fatalf("ACP turn through scoped route: %v", err)
	}
	if !strings.Contains(result.Text, "FIXTURE-OK") {
		t.Fatalf("text = %q", result.Text)
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	inference := 0
	for _, request := range gateway.requests {
		t.Logf("gateway: %s %s model=%q scoped=%t", request.Method, request.Path, request.Model, request.Scoped)
		if !strings.HasPrefix(request.Path, "/grok-bin/") {
			if request.Scoped {
				t.Errorf("scoped bearer sent outside the route: %s", request.Path)
			}
			continue
		}
		if !request.Scoped {
			t.Errorf("request without the scoped bearer: %s %s", request.Method, request.Path)
		}
		if request.Method == http.MethodPost && request.Model == "grok-fixture" {
			inference++
		}
	}
	if inference == 0 {
		t.Error("no prompt-bearing inference request reached the scoped route")
	}
	if len(gateway.leaked) > 0 {
		t.Errorf("poisoned local credential reached the gateway: %v", gateway.leaked)
	}
	assertCLIProxyExecContained(t, jailLog, false)
	// The local MCP bridge is the only tool surface, and its result returned to
	// the model through the scoped route.
	if len(toolCalls) != 1 || !strings.HasPrefix(toolCalls[0], "read_file ") || !strings.Contains(toolCalls[0], "fixture.txt") {
		t.Errorf("local tool executions = %q", toolCalls)
	}
	if strings.Join(gateway.toolNames, ",") != "search_tool,use_tool" {
		t.Errorf("tools exposed to the model = %q", gateway.toolNames)
	}
	if len(gateway.toolResults) != 2 || !strings.Contains(gateway.toolResults[1], toolMarker) {
		t.Errorf("tool result did not return through the scoped route: %q", gateway.toolResults)
	}
}

func TestRealVibeExecutableStaysOnScopedProxyRoute(t *testing.T) {
	realVibe, jail := os.Getenv(cliProxyExecVibeEnv), os.Getenv(cliProxyExecJailEnv)
	if realVibe == "" || jail == "" {
		t.Skipf("set %s and %s to run the real vibe-acp executable against a synthetic gateway", cliProxyExecVibeEnv, cliProxyExecJailEnv)
	}
	work := t.TempDir()
	jailLog := writeCLIProxyExecWrapper(t, work, vibeACPBinary, jail, realVibe)
	// Vibe unpacks read-only plugin files that TempDir cleanup cannot remove.
	t.Cleanup(func() {
		_ = filepath.WalkDir(work, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	const envCanary = "poisoned-env-key-canary"
	t.Setenv("HOME", filepath.Join(work, "user-home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(work, "cache"))
	t.Setenv("MISTRAL_API_KEY", envCanary)

	gateway := &cliProxyExecGateway{canaries: []string{envCanary}}
	server := httptest.NewServer(gateway)
	defer server.Close()
	root := server.URL + "/vibe-bin"
	registerCLIProxyBindings(t, "", root)

	provider := NewVibeBinProvider("fixture-model", map[string]string{vibeProxyRootEnv: root})
	defer provider.CleanupMCP()
	provider.SetToolExecutor(func(context.Context, string, json.RawMessage) (core.ToolOutput, error) {
		return core.ToolOutput{}, fmt.Errorf("the engine, not the provider, executes bridged tools")
	})
	const toolMarker = "local-read-file-roundtrip-marker"
	var toolCalls []string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stream, err := provider.Stream(ctx, core.Request{
		Messages: []core.Message{core.UserText("Reply with OK.")},
		Tools: []core.ToolSpec{{Name: "read_file", Description: "Read a file.", Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}, "required": []string{"path"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := core.CollectTextStream(stream, func(event core.Event) error {
		if event.Type == core.EventToolCall && event.ToolResponse != nil && event.Tool != nil {
			toolCalls = append(toolCalls, event.Tool.Name+" "+string(event.Tool.Arguments))
			event.ToolResponse <- core.ToolExecutionResponse{Result: core.TextOutput(toolMarker)}
		}
		return nil
	})
	stream.Close()
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	for _, request := range gateway.requests {
		t.Logf("gateway: %s %s model=%q scoped=%t", request.Method, request.Path, request.Model, request.Scoped)
		if !request.Scoped || !strings.HasPrefix(request.Path, "/vibe-bin/") {
			t.Errorf("request off the scoped route or without its bearer: %s %s", request.Method, request.Path)
		}
	}
	if err != nil {
		egress, _ := os.ReadFile(jailLog)
		t.Fatalf("ACP turn through scoped route: %v\negress:\n%s", err, egress)
	}
	if !strings.Contains(result.Text, "FIXTURE-OK") {
		t.Errorf("text = %q", result.Text)
	}
	// vibe-acp 3.1.2 still lists its built-in tools to the model; the fixture
	// calls the built-in read_file, which Vibe's approval policy must refuse
	// without any local execution. A bridged MCP round trip on this release
	// (through its tool-search surface) is NOT exercised here.
	t.Logf("tools exposed to the model: %q", gateway.toolNames)
	if len(toolCalls) != 0 {
		t.Errorf("unexpected local tool executions = %q", toolCalls)
	}
	if len(gateway.toolResults) == 0 || !strings.Contains(gateway.toolResults[0], "tool_denied") || strings.Contains(gateway.toolResults[0], toolMarker) {
		t.Errorf("built-in tool call was not refused: %q", gateway.toolResults)
	}
	if len(gateway.leaked) > 0 {
		t.Errorf("poisoned local credential reached the gateway: %v", gateway.leaked)
	}
	assertCLIProxyExecContained(t, jailLog, false)
}

// cursorProxyExecLegacyTurn runs a cursor-agent release that predates
// --allowed-tools from the adapter's own argument and environment builders.
// Such a release exits before any request when given the flag, so
// Provider.Stream cannot drive it and tool restriction is NOT exercised.
func cursorProxyExecLegacyTurn(t *testing.T, provider *CursorBinProvider, prompt, jailLog string, gateway *cliProxyExecGateway) {
	t.Helper()
	home, cleanup, err := provider.homeForRequest(true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := provider.writeCursorMCPConfig(home, false); err != nil {
		t.Fatal(err)
	}
	args := provider.buildCursorArgs(core.Request{}, "", nil, home)
	for i, arg := range args {
		if arg == "--allowed-tools" {
			args = append(args[:i:i], args[i+2:]...)
			break
		}
	}
	env, _, err := startCLIWireAudit("cursor-bin", provider.buildCommandEnv(home))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cursor-agent", provider.cursorProxyArgs(args)...)
	cmd.Dir, cmd.Env, cmd.Stdin = filepath.Join(home, "cwd"), env, strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"result":"OK"`) {
		egress, _ := os.ReadFile(jailLog)
		t.Fatalf("turn through scoped routes: %v\noutput: %s\nrequests: %+v\negress:\n%s", err, output, gateway.requests, egress)
	}
	if strings.Contains(string(output), cliProxyTestToken) {
		t.Error("scoped token printed by cursor-agent")
	}
}
