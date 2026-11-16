package usagetap

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func frame(op byte, fin bool, payload []byte) []byte {
	h := []byte{op, 0}
	if fin {
		h[0] |= 128
	}
	switch {
	case len(payload) < 126:
		h[1] = byte(len(payload))
	case len(payload) <= 65535:
		h[1] = 126
		h = binary.BigEndian.AppendUint16(h, uint16(len(payload)))
	default:
		h[1] = 127
		h = binary.BigEndian.AppendUint64(h, uint64(len(payload)))
	}
	return append(h, payload...)
}
func TestResponsesWebSocketFrames(t *testing.T) {
	b := []byte(responseJSON)
	half := len(b) / 2
	data := frame(1, false, b[:half])
	data = append(data, frame(9, true, []byte("ping"))...)
	data = append(data, frame(10, true, nil)...)
	data = append(data, frame(0, true, b[half:])...)
	want := New(Responses, JSON)
	want.FeedEvent(b)
	expected := want.Finish(true)
	for split := 0; split <= len(data); split++ {
		original := bytes.Clone(data)
		w := NewWebSocket(New(Responses, JSON))
		w.Feed(data[:split])
		w.Feed(data[split:])
		got := w.Finish(true)
		if got != expected {
			t.Fatalf("split %d: %+v", split, got)
		}
		if !bytes.Equal(original, data) {
			t.Fatal("mutated frame bytes")
		}
	}
	w := NewWebSocket(New(Responses, JSON))
	for _, b := range data {
		w.Feed([]byte{b})
	}
	if got := w.Finish(true); got != expected {
		t.Fatal(got)
	}
}
func TestWebSocketMaskedClientIgnored(t *testing.T) {
	payload := []byte(`{"model":"do-not-observe","usage":{"input_tokens":100000}}`)
	b := frame(1, true, payload)
	b[1] |= 128
	header := bytes.Clone(b[:2])
	header = append(header, 1, 2, 3, 4)
	for i := range payload {
		payload[i] ^= byte(i%4 + 1)
	}
	data := append(header, payload...)
	data = append(data, frame(1, true, []byte(responseJSON))...)
	w := NewWebSocket(New(Responses, JSON))
	w.Feed(data)
	if u := w.Finish(true); u.Input != 12 || u.Source != Reported {
		t.Fatal(u)
	}
}
func TestWebSocketInvalidAndOversized(t *testing.T) {
	cases := map[string][]byte{
		"compressed":               {0xc1, 0},
		"binary":                   frame(2, true, []byte(responseJSON)),
		"unexpected continuation":  frame(0, true, nil),
		"unknown opcode":           frame(3, true, nil),
		"fragmented control":       frame(9, false, nil),
		"large control":            frame(9, true, bytes.Repeat([]byte{'a'}, 126)),
		"noncanonical length":      {129, 126, 0, 1, 'a'},
		"noncanonical wide length": {129, 127, 0, 0, 0, 0, 0, 0, 0, 1, 'a'},
		"high bit length":          {129, 127, 128, 0, 0, 0, 0, 0, 0, 0},
		"nested fragment":          append(frame(1, false, []byte("{")), frame(1, true, []byte(responseJSON))...),
	}
	h := []byte{129, 127}
	h = binary.BigEndian.AppendUint64(h, MaxEventBytes+1)
	cases["oversize length"] = h
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			p := New(Responses, JSON)
			p.FeedEvent([]byte(responseJSON))
			w := NewWebSocket(p)
			w.Feed(data)
			if u := w.Finish(true); u.Source != Unknown {
				t.Fatal(u)
			}
			if len(w.message) > MaxEventBytes {
				t.Fatal("unbounded message")
			}
		})
	}
}
func TestWebSocketExtendedAndFragmentBounds(t *testing.T) {
	// Legal extended 64-bit length, still under the per-message limit.
	b := append([]byte(responseJSON[:len(responseJSON)-1]), []byte(`,"padding":"`+string(bytes.Repeat([]byte{'a'}, 65536))+`"}`)...)
	data := frame(1, true, b)
	w := NewWebSocket(New(Responses, JSON))
	for i := 0; i < len(data); i += 17 {
		w.Feed(data[i:min(i+17, len(data))])
	}
	if u := w.Finish(true); u.Source != Reported || u.Input != 12 {
		t.Fatal(u)
	}
	w = NewWebSocket(New(Responses, JSON))
	w.Feed(frame(1, false, bytes.Repeat([]byte{' '}, MaxEventBytes)))
	w.Feed(frame(0, true, []byte("x")))
	if u := w.Finish(true); u.Source != Unknown {
		t.Fatal(u)
	}
}
func TestWebSocketIncomplete(t *testing.T) {
	for _, tail := range [][]byte{{129}, {129, 126, 0}, frame(1, false, []byte("{")), {129, 5, '{'}} {
		w := NewWebSocket(New(Responses, JSON))
		w.Feed(frame(1, true, []byte(responseJSON)))
		w.Feed(tail)
		if u := w.Finish(true); u.Source != Partial || u.Input != 12 {
			t.Fatal(u)
		}
	}
}
func FuzzWebSocketFrames(f *testing.F) {
	f.Add(frame(1, true, []byte(responseJSON)), uint16(3))
	f.Add([]byte{129, 127, 255, 255, 255, 255, 255, 255, 255, 255}, uint16(1))
	f.Fuzz(func(t *testing.T, b []byte, chunk uint16) {
		if len(b) > 2*MaxEventBytes {
			t.Skip()
		}
		original := bytes.Clone(b)
		whole := NewWebSocket(New(Responses, JSON))
		whole.Feed(b)
		want := whole.Finish(true)
		w := NewWebSocket(New(Responses, JSON))
		n := int(chunk)%1024 + 1
		for i := 0; i < len(b); i += n {
			w.Feed(b[i:min(i+n, len(b))])
		}
		if u := w.Finish(true); !reflect.DeepEqual(u, want) {
			t.Fatalf("chunk mismatch: %+v %+v", u, want)
		}
		if !bytes.Equal(b, original) || len(w.message) > MaxEventBytes {
			t.Fatal("mutated or unbounded")
		}
	})
}

// TestResponsesWebSocketMultiResponseSession is the regression for Opus review
// missing proof #4. One connection carries several turns, repeated final events,
// and revised cumulative snapshots: only the latest snapshot per ID contributes.
func TestResponsesWebSocketMultiResponseSession(t *testing.T) {
	first := strings.NewReplacer(`"input_tokens":12`, `"input_tokens":10`, `"output_tokens":7`, `"output_tokens":6`, `"total_tokens":19`, `"total_tokens":16`).Replace(responseJSON)
	second := strings.NewReplacer(`"r1"`, `"r2"`, `"input_tokens":12`, `"input_tokens":3`, `"output_tokens":7`, `"output_tokens":2`, `"total_tokens":19`, `"total_tokens":5`, `"cached_tokens":4`, `"cached_tokens":1`, `"reasoning_tokens":2`, `"reasoning_tokens":0`).Replace(responseJSON)
	events := []struct {
		data                        string
		input, output, total, turns int64
	}{
		{strings.Replace(responseJSON, "response.completed", "response.in_progress", 1), -1, -1, -1, 0},
		{first, 10, 6, 16, 1},
		{first, 10, 6, 16, 1},
		{responseJSON, 12, 7, 19, 1},
		{second, 15, 9, 24, 2},
		{responseJSON, 15, 9, 24, 2},
		{second, 15, 9, 24, 2},
	}
	for _, complete := range []bool{true, false} {
		for _, chunk := range []int{1, 17, 1024} {
			p := New(Responses, JSON)
			w := NewWebSocket(p)
			for _, event := range events {
				b := []byte(event.data)
				cut := len(b) / 2
				data := frame(1, false, b[:cut])
				data = append(data, frame(9, true, []byte("ping"))...)
				data = append(data, frame(0, true, b[cut:])...)
				original := bytes.Clone(data)
				for i := 0; i < len(data); i += chunk {
					w.Feed(data[i:min(i+chunk, len(data))])
				}
				u := p.Snapshot()
				if u.Input != event.input || u.Output != event.output || u.Total != event.total || u.Turns != event.turns {
					t.Fatalf("cumulative/replay counted twice: %+v want %+v", u, event)
				}
				if !bytes.Equal(original, data) {
					t.Fatal("mutated frame bytes")
				}
			}
			w.Feed(frame(8, true, []byte{3, 232}))
			u := w.Finish(complete)
			want := Reported
			if !complete {
				want = Partial
			}
			if u.Source != want || u.Input != 15 || u.Output != 9 || u.Total != 24 || u.CachedInput != 5 || u.Reasoning != 2 || u.Turns != 2 || u.Ambiguous {
				t.Fatal(complete, chunk, u)
			}
		}
	}
}
