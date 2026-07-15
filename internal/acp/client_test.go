package acp

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

func TestClientLifecyclePreservesPromptMetadata(t *testing.T) {
	clientSide, agentSide := net.Pipe()
	defer clientSide.Close()
	defer agentSide.Close()
	client := NewClient(NewConnection(clientSide, clientSide, nil, Options{}))

	go func() {
		decoder := json.NewDecoder(agentSide)
		encoder := json.NewEncoder(agentSide)
		for {
			var request wireEnvelope
			if err := decoder.Decode(&request); err != nil {
				return
			}
			var result any
			switch request.Method {
			case "initialize":
				result = map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"loadSession":     true,
						"mcpCapabilities": map[string]any{"http": true},
					},
					"authMethods": []map[string]any{{"id": "cached_token", "name": "Cached"}},
				}
			case "authenticate":
				result = map[string]any{}
			case "session/new":
				result = map[string]any{"sessionId": "session-1"}
			case "session/prompt":
				result = map[string]any{
					"stopReason": "end_turn",
					"_meta": map[string]any{
						"inputTokens": 12, "outputTokens": 3,
					},
				}
			default:
				result = map[string]any{}
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()

	init, err := client.Initialize(context.Background(), InitializeRequest{ProtocolVersion: ProtocolVersion1})
	if err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != ProtocolVersion1 || !init.AgentCapabilities.LoadSession || !init.AgentCapabilities.MCPCapabilities.HTTP {
		t.Fatalf("initialize = %+v", init)
	}
	if len(init.AuthMethods) != 1 || init.AuthMethods[0].ID != "cached_token" {
		t.Fatalf("auth methods = %+v", init.AuthMethods)
	}
	if err := client.Authenticate(context.Background(), AuthenticateRequest{MethodID: "cached_token"}); err != nil {
		t.Fatal(err)
	}
	session, err := client.NewSession(context.Background(), NewSessionRequest{CWD: "/tmp", MCPServers: []MCPServer{}})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := client.Prompt(context.Background(), PromptRequest{
		SessionID: session.SessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.StopReason != "end_turn" || !json.Valid(prompt.Meta) || string(prompt.Meta) == "null" {
		t.Fatalf("prompt response = %+v", prompt)
	}
}
