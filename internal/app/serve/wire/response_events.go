package wire

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
)

// WriteResponseEvent writes one sequenced SSE frame to the response stream.
func WriteResponseEvent(w io.Writer, sequence int64, event string, raw []byte) error {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return err
	}
	data, err := SteeringJSON(w, payload)
	if err != nil {
		return err
	}
	event = SteeringEvent(w, event)

	b := make([]byte, 0, 4+20+1+7+len(event)+1+6+len(data)+2)
	b = append(b, "id: "...)
	b = strconv.AppendInt(b, sequence, 10)
	b = append(b, "\nevent: "...)
	b = append(b, event...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	b = append(b, "\n\n"...)
	_, err = w.Write(b)
	return err
}
