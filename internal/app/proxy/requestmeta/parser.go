package requestmeta

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"strconv"
	"strings"
)

// collect skips payload values without materializing messages, schemas or names.
// jsontext validates the entire document (including duplicate unknown members).
// All retained keys are static, all retained values are bounded scalars.
type settings struct {
	values       map[string]json.RawMessage
	bad          bool
	protocol     string
	profile      string
	wrapped      bool
	wrappedSeen  bool
	cloudRequest bool
}

var groups = map[string]bool{
	"reasoning": true, "text": true, "thinking": true, "output_config": true,
	"generationConfig": true, "generationConfig.thinkingConfig": true,
	"options": true, "chat_template_kwargs": true, "multi_agent": true,
	"prompt_cache_options": true, "tool_choice": true, "toolConfig": true,
	"toolConfig.functionCallingConfig": true,
}
var scalars = map[string]bool{
	"anthropic_version": true, "model": true, "reasoning_effort": true, "max_output_tokens": true, "max_completion_tokens": true, "max_tokens": true,
	"service_tier": true, "temperature": true, "top_p": true, "stream": true, "think": true,
	"reasoning.effort": true, "reasoning.mode": true, "reasoning.context": true, "reasoning.summary": true, "text.verbosity": true,
	"thinking.type": true, "thinking.budget_tokens": true, "output_config.effort": true,
	"generationConfig.thinkingConfig.thinkingLevel": true, "generationConfig.thinkingConfig.thinkingBudget": true,
	"generationConfig.maxOutputTokens": true, "generationConfig.temperature": true, "generationConfig.topP": true,
	"options.num_predict": true, "options.temperature": true, "options.top_p": true,
	"tool_choice": true, "tool_choice.type": true, "tool_choice.disable_parallel_tool_use": true, "parallel_tool_calls": true,
	"toolConfig.functionCallingConfig.mode": true,
	"chat_template_kwargs.thinking":         true, "chat_template_kwargs.enable_thinking": true, "thinking_token_budget": true,
	"multi_agent.enabled": true, "multi_agent.max_concurrent_subagents": true, "prompt_cache_options.mode": true, "prompt_cache_options.ttl": true,
}

func parse(data []byte, protocol, path, profile, upstream string) Metadata {
	m := Metadata{State: "invalid", Protocol: protocol}
	if !boundedDepth(data) {
		return m
	}
	d := jsontext.NewDecoder(bytes.NewBuffer(data))
	s := settings{values: make(map[string]json.RawMessage), protocol: protocol, profile: profile}
	wrapped := path == "/v1internal:generateContent" || path == "/v1internal:streamGenerateContent"
	s.wrapped = wrapped
	if s.object(d, "", wrapped) != nil {
		return m
	}
	if _, err := d.ReadToken(); err != io.EOF {
		return m
	}
	if wrapped && !s.wrappedSeen {
		return m
	}
	if protocol == "bedrock" {
		model, ok := bedrockModelFromPath(path)
		if !ok || (!knownAnthropicModel(model) && s.enum("anthropic_version", "bedrock-2023-05-31") == "") {
			return Metadata{State: "unsupported", Protocol: protocol}
		}
	}
	m.State = "observed"
	s.extract(&m, path, profile, upstream)
	if s.bad {
		m.State = "invalid"
	}
	return m
}

func (s *settings) object(d *jsontext.Decoder, prefix string, wrapped bool) error {
	t, err := d.ReadToken()
	if err != nil {
		return err
	}
	if t.Kind() != '{' {
		return io.ErrUnexpectedEOF
	}
	for d.PeekKind() != '}' {
		key, err := d.ReadToken()
		if err != nil {
			return err
		}
		name := key.String()
		if strings.Contains(name, ".") {
			if err := d.SkipValue(); err != nil {
				return err
			}
			continue
		}
		if prefix != "" {
			name = prefix + "." + name
		}
		if wrapped && prefix == "" && name == "request" {
			if d.PeekKind() == '{' {
				s.wrappedSeen = true
				s.cloudRequest = true
				err = s.object(d, "", false)
				s.cloudRequest = false
			} else {
				s.bad = true
				err = d.SkipValue()
			}
		} else if (wrapped && prefix == "" && name != "model") || (s.cloudRequest && prefix == "" && name == "model") {
			err = d.SkipValue()
		} else {
			err = s.field(d, name)
		}
		if err != nil {
			return err
		}
	}
	_, err = d.ReadToken()
	return err
}

func (s *settings) field(d *jsontext.Decoder, name string) error {
	if !s.fieldAllowed(name) {
		return d.SkipValue()
	}
	if name == "tools" {
		if s.protocol == "gemini" {
			return s.geminiTools(d)
		}
		return s.tools(d)
	}
	if groups[name] {
		if d.PeekKind() == '{' {
			return s.object(d, name, false)
		}
		if !scalars[name] || s.protocol == "messages" || s.protocol == "bedrock" {
			s.bad = true
			return d.SkipValue()
		}
	}
	if !scalars[name] {
		return d.SkipValue()
	}
	// Wrong-shaped optional fields are invalid, but are never copied as content.
	if d.PeekKind() == '{' || d.PeekKind() == '[' {
		s.bad = true
		return d.SkipValue()
	}
	raw, err := d.ReadValue()
	if err != nil {
		return err
	}
	limit := 512
	if name == "model" {
		limit = 6*128 + 2
	} // Fully escaped safe ASCII identities remain valid.
	if len(raw) > limit {
		s.bad = true
		return nil
	}
	if prior, exists := s.values[name]; exists && !bytes.Equal(prior, raw) {
		s.bad = true
		delete(s.values, name)
		return nil
	}
	s.values[name] = append(json.RawMessage(nil), raw...)
	return nil
}

func (s *settings) tools(d *jsontext.Decoder) error {
	if d.PeekKind() != '[' {
		s.bad = true
		return d.SkipValue()
	}
	if _, err := d.ReadToken(); err != nil {
		return err
	}
	var n int64
	for d.PeekKind() != ']' {
		if d.PeekKind() != '{' {
			s.bad = true
		}
		if err := d.SkipValue(); err != nil {
			return err
		}
		n++
	}
	if _, err := d.ReadToken(); err != nil {
		return err
	}
	if n > 256 {
		s.bad = true
		return nil
	}
	s.values["declared_tools"] = json.RawMessage(strconv.FormatInt(n, 10))
	return nil
}

// A cheap lexical depth pass bounds work before the validating decoder skips
// even unknown payloads. Brackets inside escaped JSON strings do not count.
func boundedDepth(data []byte) bool {
	depth := 0
	quoted, escape := false, false
	for _, b := range data {
		if quoted {
			if escape {
				escape = false
			} else if b == '\\' {
				escape = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 64 {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

func (s *settings) enum(key string, allowed ...string) string {
	raw, ok := s.values[key]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) == nil && len(v) > 0 {
		for _, a := range allowed {
			if v == a {
				return v
			}
		}
	}
	s.bad = true
	return ""
}
func (s *settings) integer(key string, min, max int64) *int64 {
	raw, ok := s.values[key]
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < min || n > max {
		s.bad = true
		return nil
	}
	return &n
}
func (s *settings) boolean(key string) *bool {
	raw, ok := s.values[key]
	if !ok {
		return nil
	}
	if string(raw) == "true" {
		v := true
		return &v
	}
	if string(raw) == "false" {
		v := false
		return &v
	}
	s.bad = true
	return nil
}
func (s *settings) number(key string, max float64) *float64 {
	raw, ok := s.values[key]
	if !ok {
		return nil
	}
	n, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || n < 0 || n > max || bytes.Equal(raw, []byte("null")) {
		s.bad = true
		return nil
	}
	return &n
}
func (s *settings) model(key string) string {
	raw, ok := s.values[key]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil || !safeModel(v) {
		s.bad = true
		return ""
	}
	return v
}
func safeModel(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-", r)) {
			return false
		}
	}
	return true
}
