package wire

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/term-llm/term-llm/internal/llm"
	"github.com/term-llm/term-llm/internal/llm/core"
)

// SteeringFingerprint identifies a structured steering mutation across retries.
func SteeringFingerprint(msg core.Message, displayText string, delivery string) (string, error) {
	parts := append([]core.Part(nil), msg.Parts...)
	for i := range parts {
		// Parsing inline attachments can materialize them at a fresh temporary path
		// on each transport retry. The content fields are the stable identity.
		if file := parts[i].FileData; parts[i].Type == core.PartFile && file != nil &&
			parts[i].Text == llm.FormatUploadedFileNotice(file.Filename, file.MediaType, parts[i].FilePath, file.SizeBytes) {
			parts[i].Text = llm.FormatUploadedFileNotice(file.Filename, file.MediaType, "", file.SizeBytes)
		}
		parts[i].ImagePath = ""
		parts[i].FilePath = ""
	}
	payload, err := json.Marshal(struct {
		Parts       []core.Part `json:"parts"`
		DisplayText string      `json:"display_text"`
		// Delivery is part of idempotency semantics: retrying the same ID with a
		// different ownership policy is a conflicting mutation, not a replay.
		Delivery string `json:"delivery"`
	}{parts, displayText, delivery})
	if err != nil {
		return "", fmt.Errorf("encode steering idempotency payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum), nil
}
