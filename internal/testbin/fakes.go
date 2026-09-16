package testbin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FakeMCP is a stand-in stdio MCP server for the tests of `sirdar mcp` and
// of the API routes behind it. It speaks the subset Sirdar's client sends —
// initialize, notifications/initialized, tools/list, tools/call — one JSON
// object per line, and nothing else.
//
// It deliberately exposes one read-shaped tool, one write-shaped tool and
// one generic passthrough, so a test can assert each verdict against a real
// server rather than a made-up tool list. FAKE_MCP_TOKEN, if set, is the
// credential the config entry handed it; echo_token hands it straight back,
// which is how the redaction is tested.
func FakeMCP() int {
	const toolList = `{"tools":[` +
		`{"name":"list_rows","description":"read some rows","inputSchema":{"type":"object","properties":{"table":{"type":"string"}}}},` +
		`{"name":"delete_rows","description":"delete some rows","inputSchema":{"type":"object"}},` +
		`{"name":"api_request","description":"send anything anywhere","inputSchema":{"type":"object"}},` +
		`{"name":"echo_token","description":"get the token back","inputSchema":{"type":"object"}}]}`

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	reply := func(id json.RawMessage, result string) {
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, result)
		out.Flush()
	}

	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			fmt.Fprintf(os.Stderr, "fake mcp server: %v\n", err)
			continue
		}
		switch req.Method {
		case "initialize":
			reply(req.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-mcp","version":"1.2.3"}}`)
		case "notifications/initialized":
			fmt.Fprintln(os.Stderr, "fake mcp server: ready")
		case "tools/list":
			reply(req.ID, toolList)
		case "tools/call":
			switch req.Params.Name {
			case "echo_token":
				reply(req.ID, mcpText("token is "+os.Getenv("FAKE_MCP_TOKEN")))
			case "list_rows":
				table, _ := req.Params.Arguments["table"].(string)
				if table == "" {
					table = "nothing"
				}
				reply(req.ID, mcpText("rows of "+table))
			default:
				reply(req.ID, mcpText("ok"))
			}
		}
	}
	return 0
}

// mcpText renders one text content block as a tools/call result.
func mcpText(s string) string {
	body, _ := json.Marshal(map[string]any{
		"content": []any{map[string]string{"type": "text", "text": s}},
	})
	return string(body)
}

// FakeClaude is a stand-in for the `claude` binary in the CLI end-to-end
// tests. It answers every session with the same canned triage document,
// whatever the prompt says.
//
// The one thing it reads from stdin is the prompt line Sirdar writes
// immediately after starting the process: consuming it keeps that write
// from blocking on a full pipe, and keeps this process from exiting into it
// and turning the write into a broken pipe. SIRDAR_FAKE_DOC names the JSON
// document to hand back as the session's structured output; newlines are
// stripped so the whole result stays on one line, as the stream-json
// protocol requires.
func FakeClaude() int {
	return fakeClaude("fake-session", 3, 0.02, 1200, 800)
}

// FakeClaudeRCA is the rca counterpart of FakeClaude: same protocol, its
// own session id and its own turn and token counts, so a test can tell the
// two runs apart in what the run records.
func FakeClaudeRCA() int {
	return fakeClaude("fake-rca-session", 4, 0.03, 2400, 1600)
}

func fakeClaude(session string, turns int, cost float64, in, out int) int {
	// The init line names the model, the way the real CLI does: the
	// workspace configures none, so this is the only place the run learns
	// what answered.
	bufio.NewReader(os.Stdin).ReadString('\n')

	path := os.Getenv("SIRDAR_FAKE_DOC")
	if path == "" {
		return Fail("SIRDAR_FAKE_DOC must name a note document")
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		return Fail("fake claude: %v", err)
	}
	doc = bytes.ReplaceAll(doc, []byte("\n"), nil)

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	fmt.Fprintf(w, `{"type":"system","subtype":"init","session_id":%q,"model":"claude-fake-5"}`+"\n", session)
	fmt.Fprintf(w,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":%d,"session_id":%q,`+
			`"result":"done","total_cost_usd":%v,"usage":{"input_tokens":%d,"output_tokens":%d},`+
			`"structured_output":%s}`+"\n",
		turns, session, cost, in, out, strings.TrimSpace(string(doc)))
	return 0
}
