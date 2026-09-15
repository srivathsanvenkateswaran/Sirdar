package run

import "encoding/json"

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
