// Package mcpclient is a minimal Model Context Protocol client speaking
// JSON-RPC 2.0 over a server subprocess's stdin/stdout, one JSON object
// per line (the 2025-06-18 spec's stdio transport). It covers exactly
// what Sirdar's own agent loop needs: the initialize handshake,
// tools/list (paginated) and tools/call, plus enough of the reverse
// direction — server-to-client requests and notifications — that a
// well-behaved server never wedges waiting on an answer Sirdar will not
// send.
//
// Everything Sirdar does not use is answered rather than ignored:
// roots/list gets the workspace root, every other server request gets a
// JSON-RPC "method not found", and unknown notifications are dropped.
package mcpclient

import (
	"encoding/json"
	"fmt"
)

// protocolVersion is the MCP revision Sirdar advertises in initialize.
// A server that speaks a different revision reports its own in the
// initialize result; Sirdar records it (ServerInfo.ProtocolVersion) but
// does not negotiate further, because it only uses tools, which have not
// changed shape across revisions.
const protocolVersion = "2025-06-18"

const (
	clientName    = "sirdar"
	clientVersion = "0.1.0-dev"
)

// message is one JSON-RPC 2.0 frame in either direction. The four
// optional halves (request: Method/Params, response: Result/Error) share
// a struct because a line's role is only known after decoding it: a
// frame with a Method is a request when it carries an ID and a
// notification when it does not, and a frame without one is a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// codeMethodNotFound is the JSON-RPC code Sirdar returns for every
// server-to-client request it does not implement.
const codeMethodNotFound = -32601

// initializeResult is the server's half of the handshake.
type initializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// ServerInfo identifies the server a Client is connected to, as reported
// by its initialize result. ProtocolVersion is the revision the server
// answered with, which may differ from the one Sirdar asked for.
type ServerInfo struct {
	Name            string
	Version         string
	ProtocolVersion string
}

// ToolInfo is one entry from tools/list. InputSchema is the tool's raw
// JSON Schema, passed through to the model untouched.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// listToolsResult is one page of tools/list. NextCursor is absent on the
// last page.
type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor"`
}

// callToolResult is the result of tools/call: a list of content items to
// flatten into one string, plus the server's own notion of whether the
// call failed (which is distinct from a JSON-RPC error, and is reported
// back to the model as tool output rather than as a client failure).
type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError"`
}

// contentItem is one element of a tools/call result's content array.
// Only Type is guaranteed; the rest depend on it.
type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	MimeType string `json:"mimeType"`
	Resource *struct {
		URI      string `json:"uri"`
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	} `json:"resource"`
}
