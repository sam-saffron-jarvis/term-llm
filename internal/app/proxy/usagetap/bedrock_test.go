package usagetap

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"reflect"
	"testing"
)

func bedrockFrame(kind, typ string, payload []byte) []byte {
	var headers []byte
	for _, h := range [][2]string{{":message-type", kind}, {":event-type", typ}, {":content-type", "application/json"}} {
		headers = append(headers, byte(len(h[0])))
		headers = append(headers, h[0]...)
		headers = append(headers, 7)
		headers = binary.BigEndian.AppendUint16(headers, uint16(len(h[1])))
		headers = append(headers, h[1]...)
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(16+len(headers)+len(payload)))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headers)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, headers...)
	frame = append(frame, payload...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}
func bedrockChunk(data string) []byte {
	p, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
	return bedrockFrame("event", "chunk", p)
}
func TestBedrockEventStream(t *testing.T) {
	start := bedrockChunk(`{"type":"message_start","message":{"model":"claude-exact","usage":{"input_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":9,"output_tokens":1}}}`)
	delta := bedrockChunk(`{"type":"message_delta","usage":{"output_tokens":8}}`)
	data := append(bytes.Clone(start), delta...)
	for split := 0; split <= len(data); split++ {
		p := NewEventStream()
		p.Feed(data[:split])
		p.Feed(data[split:])
		u := p.Finish(true)
		if u.Source != Reported || u.Input != 5 || u.CachedInput != 7 || u.CacheWrite != 9 || u.Output != 8 || u.Model != "claude-exact" {
			t.Fatal(split, u)
		}
	}
	p := NewEventStream()
	for _, b := range data {
		p.Feed([]byte{b})
	}
	if u := p.Finish(true); u.Output != 8 || u.Source != Reported {
		t.Fatal(u)
	}
	p = NewEventStream()
	p.Feed(start)
	p.Feed(delta[:20])
	if u := p.Finish(true); u.Source != Partial || u.Output != 1 {
		t.Fatal(u)
	}
}
func TestBedrockUnknown(t *testing.T) {
	valid := bedrockChunk(`{"type":"message_delta","usage":{"output_tokens":8}}`)
	badCRC := bytes.Clone(valid)
	badCRC[len(badCRC)-1] ^= 1
	badPrelude := bytes.Clone(valid)
	badPrelude[8] ^= 1
	large := make([]byte, 12)
	binary.BigEndian.PutUint32(large, MaxEventBytes+1)
	short := make([]byte, 12)
	binary.BigEndian.PutUint32(short, 10)
	headers := make([]byte, 12)
	binary.BigEndian.PutUint32(headers, 16)
	binary.BigEndian.PutUint32(headers[4:], 100)
	for name, data := range map[string][]byte{
		"CRC": badCRC, "prelude": badPrelude, "large": large, "short": short, "headers": headers,
		"base64":      bedrockFrame("event", "chunk", []byte(`{"bytes":"***"}`)),
		"envelope":    bedrockFrame("event", "chunk", []byte(`[]`)),
		"exception":   bedrockFrame("exception", "modelStreamErrorException", []byte(`{}`)),
		"converse":    bedrockFrame("event", "metadata", []byte(`{"usage":{"inputTokens":3}}`)),
		"other model": bedrockChunk(`{"usage":{"input_tokens":3}}`),
	} {
		t.Run(name, func(t *testing.T) {
			p := NewEventStream()
			p.Feed(data)
			p.Feed(valid)
			if u := p.Finish(true); u.Source != Unknown {
				t.Fatal(u)
			}
			if len(p.frame) > MaxEventBytes {
				t.Fatal("unbounded")
			}
		})
	}
}
func TestExactModelNeverRepaired(t *testing.T) {
	for _, tt := range []struct {
		proto      Protocol
		first, bad string
	}{
		{ChatCompletions, `{"model":"known","usage":{"prompt_tokens":1}}`, `{"model":"known\n","usage":{"prompt_tokens":1}}`},
		{Messages, `{"model":"known","usage":{"input_tokens":1}}`, `{"model":"known ","usage":{"output_tokens":2}}`},
		{Gemini, `{"modelVersion":"known","usageMetadata":{"promptTokenCount":1}}`, `{"modelVersion":"<known>","usageMetadata":{"promptTokenCount":1}}`},
		{Ollama, `{"model":"known","done":true,"eval_count":1}`, `{"model":"known\n","done":true,"eval_count":2}`},
	} {
		p := New(tt.proto, JSON)
		p.FeedEvent([]byte(tt.first))
		p.FeedEvent([]byte(tt.bad))
		if u := p.Finish(true); u.Model != "" {
			t.Fatal(tt.proto, u)
		}
	}
	for _, s := range []string{"known\n", "<known>", " known", "known🙂", string(bytes.Repeat([]byte{'a'}, 129))} {
		if safeModel(s) != "" {
			t.Fatal(s)
		}
	}
	if safeModel("provider/model-v1.2:latest") != "provider/model-v1.2:latest" {
		t.Fatal("changed exact valid identity")
	}
}
func FuzzBedrockEventStream(f *testing.F) {
	f.Add(bedrockChunk(`{"type":"message_delta","usage":{"output_tokens":8}}`), uint16(1))
	f.Add([]byte{0, 255, 255, 255}, uint16(8))
	f.Fuzz(func(t *testing.T, b []byte, chunk uint16) {
		if len(b) > 2*MaxEventBytes {
			t.Skip()
		}
		original := bytes.Clone(b)
		whole := NewEventStream()
		whole.Feed(b)
		want := whole.Finish(true)
		p := NewEventStream()
		n := int(chunk)%1024 + 1
		for i := 0; i < len(b); i += n {
			p.Feed(b[i:min(i+n, len(b))])
		}
		if got := p.Finish(true); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk mismatch: %+v %+v", got, want)
		}
		if !bytes.Equal(original, b) || len(p.frame) > MaxEventBytes {
			t.Fatal("mutation or unbounded")
		}
	})
}
