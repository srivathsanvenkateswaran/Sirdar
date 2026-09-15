package run

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// schemaEchoKeys are the JSON Schema header keywords a provider with no
// wire-level schema enforcement sometimes echoes back alongside its real
// answer. It is only ever shown the schema as prompt text — see
// acp.promptText — and occasionally quotes the schema's own header rather
// than answering it. A key here is dropped from a document that otherwise
// validates only when the target schema does not itself use that name for
// one of the answer's own fields (triage's root "title", for instance).
var schemaEchoKeys = []string{"$schema", "$id", "title", "description"}

// stripSchemaEcho recovers an answer from a document that also carries its
// own JSON Schema's root metadata keys.
//
// It reports schemaItself=true, with no cleaned document, when doc looks
// like the schema itself rather than an answer shaped by it — it has a root
// "properties" object, which no note or fix document's own answer ever
// does. There is nothing to reconstruct there: the model was asked for an
// answer and returned the question, so this is not treated as a strippable
// echo, only as a clearer reason to fail with.
//
// Otherwise it reports ok=true, with the cleaned document, when doc carries
// at least one of schemaEchoKeys as a root key the schema does not itself
// expect there; the caller still has to revalidate the cleaned document,
// since dropping the echoed keys is not a guarantee the rest of it answers
// the schema. ok=false with no other keys dropped means doc had nothing
// this function recognises as an echo, and the original validation error
// stands.
func stripSchemaEcho(schema, doc []byte) (cleaned []byte, schemaItself, ok bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(doc, &obj); err != nil {
		return nil, false, false
	}
	expected := schemaRootProperties(schema)

	if _, has := obj["properties"]; has && !expected["properties"] {
		return nil, true, false
	}

	dropped := false
	for _, k := range schemaEchoKeys {
		if expected[k] {
			continue // the schema's own answer legitimately uses this name
		}
		if _, has := obj[k]; has {
			delete(obj, k)
			dropped = true
		}
	}
	if !dropped {
		return nil, false, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false, false
	}
	return out, false, true
}

// schemaRootProperties is the set of root property names a draft-07 JSON
// Schema declares, so stripSchemaEcho never drops one of the answer's own
// fields on account of it sharing a name with a schema keyword.
func schemaRootProperties(schema []byte) map[string]bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	out := make(map[string]bool)
	if err := json.Unmarshal(schema, &s); err != nil {
		return out
	}
	for k := range s.Properties {
		out[k] = true
	}
	return out
}

// coerceNullStrings rewrites an explicit JSON null as "" wherever the
// schema asks for a plain string, and reports the dotted paths it touched.
// ok is false when the document had no such null and the original
// validation error stands.
//
// A model writing the answer as free text has no wire-level schema to hold
// it to the difference between "" and null, and the triage schema teaches
// it the wrong lesson by using `"type": ["string", "null"]` for the two
// fields whose absence it documents (complaintOriginal, customerIds) while
// asking for a bare string on proposedFix.remediationSql, which is empty
// on most notes and means exactly the same thing. Qwen's
// qwen-plus-character wrote null there on both the first answer and the
// retry, having been told by name which field was wrong.
//
// null and "" carry the same information for a string field — the model
// had nothing to put there — so this coerces rather than fails, and the
// caller still has to revalidate: nothing here makes a document a note.
// The fields are named in a run warning, because "the agent left this
// empty" is worth reading even when the note is otherwise good.
func coerceNullStrings(schema, doc []byte) (cleaned []byte, fields []string, ok bool) {
	var s, d any
	if err := decodeJSON(schema, &s); err != nil {
		return nil, nil, false
	}
	if err := decodeJSON(doc, &d); err != nil {
		return nil, nil, false
	}
	out, changed := coerceNode(s, d, "", &fields)
	if !changed {
		return nil, nil, false
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, false
	}
	return b, fields, true
}

// decodeJSON unmarshals into any with numbers left as json.Number, so a
// document that is re-marshalled after a coercion keeps the digits it
// arrived with rather than being round-tripped through float64.
func decodeJSON(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// coerceNode walks a schema and a value together. It only descends where
// the schema does — an object's declared properties and an array's items —
// so a value the schema says nothing about is left exactly as it is.
func coerceNode(schema, value any, path string, fields *[]string) (any, bool) {
	sm, isObject := schema.(map[string]any)
	if !isObject {
		return value, false
	}
	if value == nil {
		if sm["type"] == "string" {
			*fields = append(*fields, path)
			return "", true
		}
		return value, false
	}

	changed := false
	if props, has := sm["properties"].(map[string]any); has {
		if vm, isMap := value.(map[string]any); isMap {
			for key, child := range vm {
				ps, declared := props[key]
				if !declared {
					continue
				}
				next, hit := coerceNode(ps, child, joinPath(path, key), fields)
				if hit {
					vm[key] = next
					changed = true
				}
			}
		}
	}
	if items, has := sm["items"]; has {
		if va, isSlice := value.([]any); isSlice {
			for i, child := range va {
				next, hit := coerceNode(items, child, fmt.Sprintf("%s[%d]", path, i), fields)
				if hit {
					va[i] = next
					changed = true
				}
			}
		}
	}
	return value, changed
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
