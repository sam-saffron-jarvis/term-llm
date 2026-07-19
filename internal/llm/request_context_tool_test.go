package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

type requestContextTestTool struct {
	prepareCalls    int
	compactionCalls int
	prepareErr      error
}

func (t *requestContextTestTool) Spec() ToolSpec {
	return ToolSpec{Name: "state_tool", Schema: map[string]any{"type": "object"}}
}
func (t *requestContextTestTool) Execute(context.Context, json.RawMessage) (ToolOutput, error) {
	return TextOutput("ok"), nil
}
func (t *requestContextTestTool) Preview(json.RawMessage) string { return "" }
func (t *requestContextTestTool) PrepareRequestContext(_ context.Context, _ string, messages []Message) ([]Message, error) {
	t.prepareCalls++
	if t.prepareErr != nil {
		return nil, t.prepareErr
	}
	return append(messages, Message{Role: RoleDeveloper, Parts: []Part{{Type: PartText, Text: "restored state"}}}), nil
}
func (t *requestContextTestTool) PrepareCompactionContext(_ context.Context, _ string, result *CompactionResult) error {
	t.compactionCalls++
	result.EphemeralMessages = append(result.EphemeralMessages, Message{Role: RoleDeveloper, Parts: []Part{{Type: PartText, Text: "restored state"}}})
	return nil
}

func drainContextTestStream(t *testing.T, stream Stream) {
	t.Helper()
	defer stream.Close()
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestContextToolUsesFinalCapabilityGates(t *testing.T) {
	t.Run("available and callable", func(t *testing.T) {
		provider := NewMockProvider("mock").AddTextResponse("done")
		tool := &requestContextTestTool{}
		engine := NewEngine(provider, nil)
		engine.RegisterTool(tool)
		stream, err := engine.Stream(context.Background(), Request{Messages: []Message{UserText("work")}, Tools: []ToolSpec{tool.Spec()}})
		if err != nil {
			t.Fatal(err)
		}
		drainContextTestStream(t, stream)
		if tool.prepareCalls != 1 {
			t.Fatalf("prepare calls = %d", tool.prepareCalls)
		}
		requests := provider.RecordedRequests()
		if len(requests) != 1 || MessageText(requests[0].Messages[len(requests[0].Messages)-1]) != "restored state" {
			t.Fatalf("provider request missing restored state: %#v", requests)
		}
	})

	t.Run("filtered unavailable", func(t *testing.T) {
		provider := NewMockProvider("mock").AddTextResponse("done")
		tool := &requestContextTestTool{}
		engine := NewEngine(provider, nil)
		engine.RegisterTool(tool)
		engine.SetAllowedToolsFilter([]string{})
		stream, err := engine.Stream(context.Background(), Request{Messages: []Message{UserText("work")}, Tools: []ToolSpec{tool.Spec()}})
		if err != nil {
			t.Fatal(err)
		}
		drainContextTestStream(t, stream)
		if tool.prepareCalls != 0 {
			t.Fatalf("prepare calls = %d, want 0", tool.prepareCalls)
		}
	})

	t.Run("provider cannot call tools", func(t *testing.T) {
		provider := NewMockProvider("mock").WithCapabilities(Capabilities{}).AddTextResponse("done")
		tool := &requestContextTestTool{}
		engine := NewEngine(provider, nil)
		engine.RegisterTool(tool)
		stream, err := engine.Stream(context.Background(), Request{Messages: []Message{UserText("work")}, Tools: []ToolSpec{tool.Spec()}})
		if err != nil {
			t.Fatal(err)
		}
		drainContextTestStream(t, stream)
		if tool.prepareCalls != 0 {
			t.Fatalf("prepare calls = %d, want 0", tool.prepareCalls)
		}
	})
}

func TestRequestContextFailureDoesNotBlockStream(t *testing.T) {
	provider := NewMockProvider("mock").AddTextResponse("done")
	tool := &requestContextTestTool{prepareErr: errors.New("state store unavailable")}
	engine := NewEngine(provider, nil)
	engine.RegisterTool(tool)

	stream, err := engine.Stream(context.Background(), Request{
		Messages: []Message{UserText("work")},
		Tools:    []ToolSpec{tool.Spec()},
	})
	if err != nil {
		t.Fatalf("Stream returned optional context error: %v", err)
	}
	drainContextTestStream(t, stream)
	requests := provider.RecordedRequests()
	if len(requests) != 1 || len(requests[0].Messages) != 1 || MessageText(requests[0].Messages[0]) != "work" {
		t.Fatalf("provider request = %#v, want original messages without restoration", requests)
	}
}

func TestCompactionResultActiveMessages(t *testing.T) {
	durable := []Message{UserText("summary")}
	result := &CompactionResult{NewMessages: durable}
	active := result.ActiveMessages()
	if len(active) != 1 || &active[0] != &durable[0] {
		t.Fatal("empty ephemeral messages should return NewMessages directly")
	}
	result.EphemeralMessages = []Message{{Role: RoleDeveloper, Parts: []Part{{Type: PartText, Text: "plan"}}}}
	active = result.ActiveMessages()
	if len(active) != 2 || active[0].Role != RoleDeveloper || MessageText(active[0]) != "plan" || len(result.NewMessages) != 1 {
		t.Fatalf("ActiveMessages() = %#v", active)
	}
}
