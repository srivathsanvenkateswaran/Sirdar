// Package note validates, renders, and files the three note kinds a Sirdar
// run produces: triage, RCA, and resolution.
package note

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// Kind identifies which of the three note types a document or template is
// for.
type Kind string

const (
	Triage     Kind = "triage"
	RCA        Kind = "rca"
	Resolution Kind = "resolution"
)

var printer = message.NewPrinter(language.English)

var (
	triageSchemaOnce sync.Once
	triageSchema     *jsonschema.Schema
	triageSchemaErr  error

	rcaSchemaOnce sync.Once
	rcaSchema     *jsonschema.Schema
	rcaSchemaErr  error
)

func compileTriageSchema() (*jsonschema.Schema, error) {
	triageSchemaOnce.Do(func() {
		triageSchema, triageSchemaErr = compileSchema("triage.json", prompt.TriageSchema)
	})
	return triageSchema, triageSchemaErr
}

func compileRCASchema() (*jsonschema.Schema, error) {
	rcaSchemaOnce.Do(func() {
		rcaSchema, rcaSchemaErr = compileSchema("rca.json", prompt.RCASchema)
	})
	return rcaSchema, rcaSchemaErr
}

// compileSchema compiles the draft-07 schema in raw, registered under name,
// into a *jsonschema.Schema ready to validate instances against.
func compileSchema(name string, raw []byte) (*jsonschema.Schema, error) {
	res, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("note: parse schema %s: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(name, res); err != nil {
		return nil, fmt.Errorf("note: add schema resource %s: %w", name, err)
	}
	sch, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("note: compile schema %s: %w", name, err)
	}
	return sch, nil
}

// Validate checks doc against the JSON Schema for kind. Triage validates
// against prompt.TriageSchema; RCA and Resolution both validate the
// combined rca+resolution document against prompt.RCASchema, since an rca
// run produces one JSON document with two top-level objects. The returned
// error, when non-nil, joins every leaf schema violation as
// "<instance location>: <message>", one per line, prefixed "note does not
// match schema:".
func Validate(kind Kind, doc []byte) error {
	var (
		sch *jsonschema.Schema
		err error
	)
	switch kind {
	case Triage:
		sch, err = compileTriageSchema()
	case RCA, Resolution:
		sch, err = compileRCASchema()
	default:
		return fmt.Errorf("note: unknown kind %q", kind)
	}
	if err != nil {
		return err
	}

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("note: parse document: %w", err)
	}

	verr := sch.Validate(inst)
	if verr == nil {
		return nil
	}
	ve, ok := verr.(*jsonschema.ValidationError)
	if !ok {
		return fmt.Errorf("note does not match schema: %v", verr)
	}

	leaves := leafCauses(ve)
	lines := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		lines = append(lines, fmt.Sprintf("%s: %s", instanceLocation(leaf.InstanceLocation), leaf.ErrorKind.LocalizedString(printer)))
	}
	return fmt.Errorf("note does not match schema:\n%s", strings.Join(lines, "\n"))
}

// leafCauses walks e's cause tree and returns every node with no further
// causes: the individual schema-keyword failures, rather than the group and
// reference wrappers the library uses to nest them.
func leafCauses(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leafCauses(c)...)
	}
	return out
}

// instanceLocation renders a validation error's instance location as a
// dotted path, e.g. []string{"rootCause", "confidence"} -> "rootCause.confidence".
func instanceLocation(loc []string) string {
	if len(loc) == 0 {
		return "(root)"
	}
	return strings.Join(loc, ".")
}
