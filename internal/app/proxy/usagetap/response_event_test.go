package usagetap

import (
	"bytes"
	"strings"
	"testing"
)

// Compare an assembled HTTP SSE event with the unoptimized full-event decoder.
// Start with distinct reported evidence so a lost malformed, partial, or second
// metered event changes the final snapshot instead of just remaining unknown.
func FuzzResponsesSSEDispatch(f *testing.F) {
	const rootUsage = `"usage":{"total_tokens":7}`
	const response = `"response":{"id":"r2","usage":{"total_tokens":8}}`
	for _, data := range []string{
		responseJSON, `{"type":"response.done",` + response + `}`, `{"type":"response.failed",` + response + `}`, `{"type":"response.incomplete",` + response + `}`,
		" \t{\n \"delta\" : {\"type\":\"response.completed\"}, \"type\" : \"future.event\" }\r\n",
		`{"delta":"\"type\":\"response.output_text.delta\"",` + rootUsage + `}`,
		`{"type":"delta","t\u0079pe":"response.done",` + response + `}`,
		`{"type":"response.done","type":"delta",` + response + `}`,
		`{"t\u0079pe":"response.\u0064one",` + response + `}`,
		`{"type":"delta","type":null,` + rootUsage + `}`,
		`{"TYPE":"delta",` + rootUsage + `}`, `{"type":null,` + rootUsage + `}`,
		`{"type":123,` + rootUsage + `}`, `{"type":{},` + rootUsage + `}`, `{"type":"",` + rootUsage + `}`,
		`{"type":"response.created",` + rootUsage + `}`, `{"type":"unknown","x":[1,]}`,
		`{"type":"delta","x":"\q"}`, `{"type":"delta"}{}`, `{"type":"delta"`,
		`{"type":"delta","x":"` + "\xff" + `"}`, "\u00a0{}\u00a0", " \t", `[DONE]`, `null`, `[]`,
	} {
		f.Add([]byte(data))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Framing has already rejected larger events before dispatch.
		if len(data) > MaxEventBytes {
			t.Skip()
		}
		fast, full := New(Responses, SSE), New(Responses, SSE)
		for _, p := range []*Parser{fast, full} {
			p.FeedEvent([]byte(strings.Replace(responseJSON, `"r1"`, `"seed"`, 1)))
		}
		fast.event = bytes.Clone(data) // dispatch clears its owned scratch buffer
		fast.dispatch()
		if len(bytes.TrimSpace(data)) != 0 {
			full.FeedEvent(data)
		}
		if got, want := fast.Finish(true), full.Finish(true); got != want {
			t.Fatalf("dispatch: %+v; full decode: %+v", got, want)
		}
	})
}
