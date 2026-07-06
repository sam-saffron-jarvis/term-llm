package llm

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

type sseDecoderOptions struct {
	RequireDone bool
	Transport   string
	Terminal    string
}

type sseDecoder struct {
	reader      *bufio.Reader
	requireDone bool
	transport   string
	terminal    string
	sawDone     bool
	closed      bool
	pendingErr  error
}

func newSSEDecoder(r io.Reader, opts sseDecoderOptions) *sseDecoder {
	reader, ok := r.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(r)
	}
	transport := opts.Transport
	if transport == "" {
		transport = "SSE"
	}
	terminal := opts.Terminal
	if terminal == "" {
		terminal = string(sseDoneData)
	}
	return &sseDecoder{
		reader:      reader,
		requireDone: opts.RequireDone,
		transport:   transport,
		terminal:    terminal,
	}
}

func (d *sseDecoder) DoneSeen() bool {
	return d != nil && d.sawDone
}

func (d *sseDecoder) Next() (eventType string, data []byte, err error) {
	if d.pendingErr != nil {
		err := d.pendingErr
		d.pendingErr = nil
		return "", nil, err
	}
	if d.closed {
		return "", nil, io.EOF
	}

	for {
		eventType, data, eof, err := readSSEEventBytes(d.reader)
		if err != nil {
			return "", nil, err
		}
		if eof && eventType == "" && len(data) == 0 {
			d.closed = true
			if d.requireDone && !d.sawDone {
				return "", nil, d.missingDoneError()
			}
			return "", nil, io.EOF
		}
		if bytes.Equal(data, sseDoneData) {
			d.sawDone = true
			d.closed = true
			return "", nil, io.EOF
		}
		if len(bytes.TrimSpace(data)) == 0 {
			if eof {
				d.closed = true
				if d.requireDone && !d.sawDone {
					return "", nil, d.missingDoneError()
				}
				return "", nil, io.EOF
			}
			continue
		}
		if eof {
			d.closed = true
			if d.requireDone && !d.sawDone {
				d.pendingErr = d.missingDoneError()
			} else {
				d.pendingErr = io.EOF
			}
		}
		return eventType, data, nil
	}
}

func (d *sseDecoder) missingDoneError() error {
	return &StreamIncompleteError{Transport: d.transport, Terminal: d.terminal}
}

func readSSELine(reader *bufio.Reader) (line string, eof bool, err error) {
	lineBytes, eof, err := readSSELineBytes(reader)
	if err != nil {
		return "", false, err
	}
	return string(lineBytes), eof, nil
}

func readSSELineBytes(reader *bufio.Reader) (line []byte, eof bool, err error) {
	var owned []byte
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if len(chunk) > 0 {
			if owned != nil {
				owned = append(owned, chunk...)
			} else if errors.Is(readErr, bufio.ErrBufferFull) {
				owned = append(owned, chunk...)
			} else {
				line = chunk
			}
		}

		switch {
		case readErr == nil:
			if owned != nil {
				line = owned
			}
			return bytes.TrimRight(line, "\r\n"), false, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF):
			if owned != nil {
				line = owned
			} else if len(chunk) > 0 {
				line = chunk
			}
			return bytes.TrimRight(line, "\r\n"), true, nil
		default:
			return nil, false, readErr
		}
	}
}

var (
	sseEventField = []byte("event")
	sseDataField  = []byte("data")
	sseDoneData   = []byte("[DONE]")
)

func readSSEEvent(reader *bufio.Reader) (eventType, data string, eof bool, err error) {
	var dataBuilder strings.Builder
	dataLines := 0

	appendData := func(value []byte) {
		if dataLines == 0 {
			data = string(value)
			dataLines = 1
			return
		}
		if dataLines == 1 {
			dataBuilder.WriteString(data)
			data = ""
		}
		dataBuilder.WriteByte('\n')
		dataBuilder.Write(value)
		dataLines++
	}

	for {
		line, lineEOF, err := readSSELineBytes(reader)
		if err != nil {
			return "", "", false, err
		}
		if len(line) == 0 {
			if dataLines > 1 {
				data = dataBuilder.String()
			}
			return eventType, data, lineEOF, nil
		}
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field, value := line[:i], line[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			switch {
			case bytes.Equal(field, sseEventField):
				eventType = string(value)
			case bytes.Equal(field, sseDataField):
				appendData(value)
			}
		}
		if lineEOF {
			if dataLines > 1 {
				data = dataBuilder.String()
			}
			return eventType, data, true, nil
		}
	}
}

func readSSEEventBytes(reader *bufio.Reader) (eventType string, data []byte, eof bool, err error) {
	for {
		line, lineEOF, err := readSSELineBytes(reader)
		if err != nil {
			return "", nil, false, err
		}
		if len(line) == 0 {
			return eventType, data, lineEOF, nil
		}
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field, value := line[:i], line[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			switch {
			case bytes.Equal(field, sseEventField):
				eventType = string(value)
			case bytes.Equal(field, sseDataField):
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, value...)
			}
		}
		if lineEOF {
			return eventType, data, true, nil
		}
	}
}
