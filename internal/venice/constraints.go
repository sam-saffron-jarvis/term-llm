package venice

import (
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Optional upstream metadata is not a schema guarantee. An unknown shape or
// entry disables that constraint dimension rather than poisoning validation.
func constraintValues(raw json.RawMessage, normalize func(string) (string, bool)) []string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	result := []string{}
	for _, v := range values {
		var text string
		switch item := v.(type) {
		case string:
			text = item
		case json.Number:
			text = item.String()
		default:
			return nil
		}
		normalized, ok := normalize(text)
		if !ok {
			return nil
		}
		result = append(result, normalized)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func normalizeSeconds(s string) (string, bool) {
	n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "s"), 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return "", false
	}
	return strconv.FormatFloat(n, 'f', -1, 64) + "s", true
}

var catalogResolution = regexp.MustCompile(`^[1-9][0-9]*(p|k)$`)

func normalizeResolution(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, catalogResolution.MatchString(s)
}
func normalizedStrings(values []string, normalize func(string) (string, bool)) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		n, ok := normalize(v)
		if !ok {
			return nil
		}
		out = append(out, n)
	}
	return out
}

type catalogConstraints struct {
	ModelType   string          `json:"model_type"`
	Durations   json.RawMessage `json:"durations"`
	Resolutions json.RawMessage `json:"resolutions"`
}

func parseConstraints(raw json.RawMessage) *catalogConstraints {
	var c catalogConstraints
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	return &c
}
