package llm

import (
	"encoding/json"
	"reflect"
	"sync"
)

type toolSchemaJSONCacheEntry struct {
	// Hold a strong reference to the immutable schema map so its identity cannot
	// be reused for a different schema while the cached JSON remains live.
	schema map[string]interface{}
	once   sync.Once
	raw    json.RawMessage
	err    error
}

var toolSchemaJSONCache sync.Map

// cachedToolSchemaJSON returns the JSON encoding of an immutable ToolSpec schema.
// The cache is keyed by schema map identity, matching ToolRegistry's convention of
// sharing schema maps across repeated request turns. Entries hold a strong schema
// reference so that map identity cannot be reused while cached bytes are live.
//
// The returned RawMessage shares cached backing bytes and must be treated as
// immutable; provider request builders only pass it to JSON encoders.
func cachedToolSchemaJSON(schema map[string]interface{}) (json.RawMessage, error) {
	var key uintptr
	if schema != nil {
		key = reflect.ValueOf(schema).Pointer()
	}

	cached, _ := toolSchemaJSONCache.LoadOrStore(key, &toolSchemaJSONCacheEntry{schema: schema})
	entry := cached.(*toolSchemaJSONCacheEntry)
	entry.once.Do(func() {
		b, err := json.Marshal(schema)
		if err != nil {
			entry.err = err
			return
		}
		entry.raw = json.RawMessage(b)
	})
	if entry.err != nil {
		return nil, entry.err
	}
	return entry.raw, nil
}
