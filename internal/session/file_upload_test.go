package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestMessagePreservesFileDataForStorageReplay(t *testing.T) {
	const b64 = "YSxiCg=="
	msg := NewMessage("sess_test", llm.Message{Role: llm.RoleUser, Parts: []llm.Part{{
		Type: llm.PartFile,
		Text: llm.FormatEmbeddedFileText("data.csv", "text/csv", "a,b\n"),
		FileData: &llm.ToolFileData{
			MediaType: "text/csv",
			Base64:    b64,
			Filename:  "data.csv",
			SizeBytes: 4,
		},
	}}}, 0)

	if msg.Parts[0].FileData == nil {
		t.Fatal("FileData was removed; want upload data preserved for session replay")
	}
	if msg.Parts[0].FileData.Base64 != b64 {
		t.Fatalf("stored base64 = %q, want %q", msg.Parts[0].FileData.Base64, b64)
	}
	if !strings.Contains(msg.TextContent, "a,b") {
		t.Fatalf("TextContent = %q, want file text fallback", msg.TextContent)
	}

	partsJSON, err := msg.PartsJSON()
	if err != nil {
		t.Fatalf("PartsJSON: %v", err)
	}
	if !strings.Contains(partsJSON, b64) {
		t.Fatalf("parts json did not preserve base64 data: %s", partsJSON)
	}

	var roundTrip Message
	if err := roundTrip.SetPartsFromJSON(partsJSON); err != nil {
		t.Fatalf("SetPartsFromJSON: %v", err)
	}
	encoded, _ := json.Marshal(roundTrip.Parts)
	if !strings.Contains(string(encoded), b64) {
		t.Fatalf("round-trip parts did not preserve base64: %s", encoded)
	}
}
