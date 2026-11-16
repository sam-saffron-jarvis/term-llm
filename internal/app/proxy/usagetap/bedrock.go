package usagetap

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/term-llm/term-llm/internal/awsauth"
)

// EventStream incrementally observes Bedrock Anthropic invoke event streams.
// Only complete frames of at most MaxEventBytes reach the existing CRC/header
// decoder. No credential, signing, reader goroutine or provider code is used.
// Converse and non-Anthropic schemas remain unknown; model names are never
// inferred from the URL (Anthropic Bedrock often omits the response model).
type EventStream struct {
	parser   *Parser
	frame    []byte
	want     int
	disabled bool
}

func NewEventStream() *EventStream { return &EventStream{parser: New(Messages, JSON), want: 12} }
func (e *EventStream) fail()       { e.parser.MarkUnknown(); e.disabled = true; e.frame = nil }

func (e *EventStream) Feed(data []byte) {
	if e.disabled || e.parser.finished {
		return
	}
	for len(data) > 0 && !e.disabled {
		n := min(e.want-len(e.frame), len(data))
		e.frame = append(e.frame, data[:n]...)
		data = data[n:]
		if len(e.frame) < e.want {
			continue
		}
		if e.want == 12 {
			total := binary.BigEndian.Uint32(e.frame[:4])
			headers := binary.BigEndian.Uint32(e.frame[4:8])
			if total < 16 || total > MaxEventBytes || headers > total-16 {
				e.fail()
				return
			}
			e.want = int(total)
			continue
		}
		e.decodeFrame()
		e.frame = nil
		e.want = 12
	}
}
func (e *EventStream) decodeFrame() {
	msg, err := awsauth.NewEventStreamDecoder(bytes.NewReader(e.frame)).Next()
	if err != nil {
		e.fail()
		return
	}
	typ, _ := msg.Header(":event-type")
	kind, _ := msg.Header(":message-type")
	if kind != "event" || typ != "chunk" {
		e.fail()
		return
	}
	var chunk struct {
		Bytes string `json:"bytes"`
	}
	if json.Unmarshal(msg.Payload, &chunk) != nil || chunk.Bytes == "" || base64.StdEncoding.DecodedLen(len(chunk.Bytes)) > MaxEventBytes {
		e.fail()
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(chunk.Bytes)
	if err != nil {
		e.fail()
		return
	}
	// Do not mistake another Bedrock model family's usage schema for Messages.
	o := obj(decoded)
	typ = textValue(o["type"])
	switch typ {
	case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping":
		e.parser.FeedEvent(decoded)
	default:
		e.fail()
	}
}

func (e *EventStream) Finish(complete bool) Usage {
	if len(e.frame) != 0 {
		complete = false
	}
	e.frame = nil
	return e.parser.Finish(complete)
}
