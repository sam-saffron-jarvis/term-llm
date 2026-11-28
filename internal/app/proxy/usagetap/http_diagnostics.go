package usagetap

import "encoding/json"

// Cancellation is terminal diagnostic evidence, but deliberately does not alter
// terminalResponseType or the established usage-accounting/correlation rules.
func diagnosticTerminalResponseType(typ string) bool {
	return terminalResponseType(typ) || typ == "response.cancelled" || typ == "response.canceled"
}

func (p *Parser) httpResponseDiagnostics(event object) {
	typ := textValue(event["type"])
	if typ != "" && !diagnosticTerminalResponseType(typ) {
		return
	}
	response := event
	if typ != "" {
		response = obj(event["response"])
		if response == nil {
			p.diagnostics.invalid = true
			return
		}
	}
	previous := p.diagnostics
	next := responseDiagnosticsFor(response, typ)
	// Diagnostic uncertainty is monotonic across an HTTP stream, independently
	// of usage presence. A usage-free terminal cannot erase an earlier ambiguity.
	next.invalid = next.invalid || previous.invalid || p.diagnosticEventInvalid
	if previous.meta.Status != "" && next.meta.Status != "" && previous.meta.Status != next.meta.Status {
		next.invalid = true
	}
	if previous.meta.IncompleteReason != "" && next.meta.IncompleteReason != "" && previous.meta.IncompleteReason != next.meta.IncompleteReason {
		next.invalid = true
	}
	if !p.noteDiagnosticResponseID(response) {
		next.invalid = true
	}
	p.diagnostics = next
}

// These bounded identities remain in memory only. Missing IDs occupy one slot;
// invalid/oversized IDs and overflow are diagnostic uncertainty, never usage loss.
func (p *Parser) noteDiagnosticResponseID(response object) bool {
	var id string
	if raw, exists := response["id"]; exists {
		if isJSONNull(raw) || json.Unmarshal(raw, &id) != nil || id == "" || len(id) > maxResponseIDBytes {
			return false
		}
	}
	if p.diagnosticResponseIDs == nil {
		p.diagnosticResponseIDs = make(map[string]struct{})
	}
	if _, exists := p.diagnosticResponseIDs[id]; !exists {
		if len(p.diagnosticResponseIDs) == maxResponseIDs {
			return false
		}
		p.diagnosticResponseIDs[id] = struct{}{}
	}
	return len(p.diagnosticResponseIDs) <= 1
}
