package usagetap

import "encoding/binary"

// WebSocket observes raw RFC6455 server frames. It never writes or changes
// bytes. Only uncompressed, unmasked text messages are usage events. Masked
// client frames and control payloads are skipped without retaining them.
// Negotiated compression must also be marked unknown by the caller, even if
// no RSV1 frame has yet arrived. Invalid framing permanently disables the tap.
// Like Parser, this type requires a single owner.
type WebSocket struct {
	parser                                     *Parser
	header                                     [14]byte
	have, need                                 int
	remaining                                  uint64
	opcode                                     byte
	fin, masked, discard, disabled, fragmented bool
	message                                    []byte
}

// NewWebSocket wraps a parser intended for FeedEvent (normally Responses).
func NewWebSocket(p *Parser) *WebSocket { return &WebSocket{parser: p, need: 2} }
func (w *WebSocket) fail()              { w.parser.MarkUnknown(); w.disabled = true; w.message = nil }

// Feed observes arbitrary splits, including within the frame header.
func (w *WebSocket) Feed(data []byte) {
	if w.disabled || w.parser.finished {
		return
	}
	for len(data) > 0 && !w.disabled {
		if w.remaining == 0 {
			n := min(w.need-w.have, len(data))
			copy(w.header[w.have:], data[:n])
			w.have += n
			data = data[n:]
			if w.have < w.need {
				continue
			}
			if w.need == 2 {
				size := w.header[1] & 127
				if size == 126 {
					w.need += 2
				} else if size == 127 {
					w.need += 8
				}
				if w.header[1]&128 != 0 {
					w.need += 4
				}
				if w.have < w.need {
					continue
				}
			}
			w.startFrame()
			if w.disabled {
				return
			}
			if w.remaining == 0 {
				w.endFrame()
				continue
			}
		}
		n := len(data)
		if uint64(n) > w.remaining {
			n = int(w.remaining)
		}
		if !w.discard {
			w.message = append(w.message, data[:n]...)
		}
		data = data[n:]
		w.remaining -= uint64(n)
		if w.remaining == 0 {
			w.endFrame()
		}
	}
}
func (w *WebSocket) startFrame() {
	h := w.header
	w.fin = h[0]&128 != 0
	w.opcode = h[0] & 15
	w.masked = h[1]&128 != 0
	length := uint64(h[1] & 127)
	if length == 126 {
		length = uint64(binary.BigEndian.Uint16(h[2:4]))
		if length < 126 {
			w.fail()
			return
		}
	}
	if h[1]&127 == 127 {
		length = binary.BigEndian.Uint64(h[2:10])
		if length < 65536 || length>>63 != 0 {
			w.fail()
			return
		}
	}
	w.remaining = length
	w.discard = true
	if h[0]&112 != 0 {
		w.fail()
		return
	}
	if w.opcode >= 8 {
		if !w.fin || length > 125 || (w.opcode != 8 && w.opcode != 9 && w.opcode != 10) {
			w.fail()
		}
		return
	}
	if w.masked {
		return
	} // A client-direction frame is not response evidence.
	switch w.opcode {
	case 1:
		if w.fragmented {
			w.fail()
			return
		}
		w.message = nil
	case 0:
		if !w.fragmented {
			w.fail()
			return
		}
	case 2:
		w.fail()
		return // Opaque binary provider protocol.
	default:
		w.fail()
		return
	}
	if length > uint64(MaxEventBytes-len(w.message)) {
		w.fail()
		return
	}
	w.discard = false
}
func (w *WebSocket) endFrame() {
	if !w.discard {
		if w.fin {
			w.parser.FeedEvent(w.message)
			w.message = nil
			w.fragmented = false
		} else {
			w.fragmented = true
		}
	}
	w.have = 0
	w.need = 2
}

// Finish marks truncated headers, frames or fragmented messages incomplete.
func (w *WebSocket) Finish(complete bool) Usage {
	if w.have != 0 || w.remaining != 0 || w.fragmented {
		complete = false
	}
	w.message = nil
	return w.parser.Finish(complete)
}
