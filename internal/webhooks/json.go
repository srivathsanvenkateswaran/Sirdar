package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decode reads a webhook body into the generic shape the extractors walk.
// Numbers are kept as json.Number: an Azure DevOps work item id or a
// HubSpot object id is a 64-bit integer, and a float64 would round the
// large ones into a key that names a different ticket.
func decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	return v, nil
}

// decodeObject reads a body that must be a JSON object.
func decodeObject(raw []byte) (map[string]any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: the body is not a JSON object", ErrBadPayload)
	}
	return obj, nil
}

// at walks a path of object keys and returns what it finds, or nil. A
// segment that is not an object, or a key that is absent, ends the walk.
func at(v any, path ...string) any {
	for _, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[key]
	}
	return v
}

// text renders a scalar as the string a key or an assignee is made of.
// Anything that is not a scalar — an object, a list, a null — is empty,
// so a caller can treat "" as "the payload does not say".
func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// textAt is at followed by text, which is how most fields are read.
func textAt(v any, path ...string) string { return text(at(v, path...)) }

// firstText returns the first non-empty of several candidate paths, so an
// extractor can name the field it wants and the fallbacks it will accept.
func firstText(v any, paths ...[]string) string {
	for _, p := range paths {
		if s := textAt(v, p...); s != "" {
			return s
		}
	}
	return ""
}

// reencode returns the JSON of one element of a batched payload, for the
// Raw of the trigger read out of it.
func reencode(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
