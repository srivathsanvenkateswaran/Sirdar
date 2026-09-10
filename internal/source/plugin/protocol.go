// Package plugin implements Sirdar's out-of-process source adapter
// protocol: a line-delimited JSON request/response exchange over an
// adapter subprocess's stdin/stdout. Adapters speak the protocol
// described in docs/adapters.md and may be written in any language.
package plugin

import (
	"encoding/json"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

// Request is one line sent to an adapter on its stdin.
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"` // describe | tracker.get | tracker.list | helpdesk.get | helpdesk.threads | helpdesk.attachments | shutdown
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one line an adapter writes to its stdout in reply to a
// Request with the same ID.
type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *source.Error   `json:"error,omitempty"`
}

// Describe is the result of the "describe" method: the adapter's name,
// the roles it fulfils ("tracker", "helpdesk", or both), and a version
// string.
type Describe struct {
	Name    string   `json:"name"`
	Roles   []string `json:"roles"`
	Version string   `json:"version"`
}
