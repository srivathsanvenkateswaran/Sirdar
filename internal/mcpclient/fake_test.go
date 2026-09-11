package mcpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEnv makes the test binary re-exec itself as an MCP server instead
// of running tests: TestMain reads it before m.Run, so `go test` builds
// the fake server for free and no fixture binary has to be compiled or
// checked in. "hang" is the variant that answers the handshake and then
// stops responding, so Close's kill path can be timed.
const fakeEnv = "SIRDAR_MCP_FAKE"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeEnv) {
	case "":
		os.Exit(m.Run())
	case "hang":
		runFake(true)
	default:
		runFake(false)
	}
}

// fake is the server side of the test: a line-oriented JSON-RPC loop
// with a one-message pushback queue, which is what lets it issue
// requests of its own (roots/list, sampling/createMessage) and wait for
// the client's answer without losing the client requests that arrive
// while it waits.
type fake struct {
	sc    *bufio.Scanner
	queue []message
	roots string
}

func runFake(hang bool) {
	fmt.Fprintln(os.Stderr, "fake mcp server: listening on stdio")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	f := &fake{sc: sc}

	for {
		m, ok := f.next()
		if !ok {
			os.Exit(0)
		}
		switch m.Method {
		case "initialize":
			f.write(message{ID: m.ID, Result: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fake-mcp","version":"9.9.9"}}`)})
			if hang {
				// Never read stdin again, never exit: only Close's
				// process-group kill can stop this.
				time.Sleep(10 * time.Minute)
				os.Exit(0)
			}
		case "notifications/initialized":
			// An unknown notification the client must ignore, then a
			// request it is expected to refuse with -32601. Both are
			// reported on stderr so the test can assert on them.
			f.write(message{Method: "notifications/message", Params: json.RawMessage(`{"level":"info","data":"warming up"}`)})
			f.write(message{ID: json.RawMessage(`"srv-sampling"`), Method: "sampling/createMessage", Params: json.RawMessage(`{"messages":[]}`)})
			if resp, ok := f.await(`"srv-sampling"`); ok && resp.Error != nil {
				fmt.Fprintf(os.Stderr, "fake mcp server: sampling rejected: %d\n", resp.Error.Code)
			}
		case "tools/list":
			f.listTools(m)
		case "tools/call":
			f.callTool(m)
		default:
			if len(m.ID) > 0 {
				f.write(message{ID: m.ID, Error: &rpcError{Code: codeMethodNotFound, Message: "no such method"}})
			}
		}
	}
}

// listTools serves two pages so the client's cursor loop is exercised:
// echo and fail first, slow second.
func (f *fake) listTools(m message) {
	var p struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch p.Cursor {
	case "":
		f.write(message{ID: m.ID, Result: json.RawMessage(`{"tools":[
			{"name":"echo","description":"echo a message","inputSchema":{"type":"object","properties":{"message":{"type":"string"}}}},
			{"name":"fail","description":"always fails","inputSchema":{"type":"object"}}
		],"nextCursor":"page2"}`)})
	case "page2":
		f.write(message{ID: m.ID, Result: json.RawMessage(`{"tools":[
			{"name":"slow","description":"sleeps","inputSchema":{"type":"object"}}
		]}`)})
	default:
		f.write(message{ID: m.ID, Result: json.RawMessage(`{"tools":[]}`)})
	}
}

func (f *fake) callTool(m message) {
	if f.roots == "" {
		f.askRoots()
	}
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Message string `json:"message"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(m.Params, &p)

	switch p.Name {
	case "echo":
		items := []map[string]any{
			{"type": "text", "text": "roots: " + f.roots},
			{"type": "text", "text": "echo: " + p.Arguments.Message},
			{"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="},
		}
		f.result(m.ID, map[string]any{"content": items})
	case "fail":
		f.result(m.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "boom: the tool failed"}},
			"isError": true,
		})
	case "slow":
		time.Sleep(2 * time.Second)
		f.result(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "slow finished"}}})
	default:
		f.write(message{ID: m.ID, Error: &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}})
	}
}

// askRoots issues the server-to-client roots/list request before the
// first tool call, the way servers that scope themselves to the
// workspace do, and remembers what came back.
func (f *fake) askRoots() {
	f.write(message{ID: json.RawMessage(`"srv-roots"`), Method: "roots/list"})
	resp, ok := f.await(`"srv-roots"`)
	if !ok || resp.Error != nil {
		f.roots = "<none>"
		return
	}
	var r struct {
		Roots []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil || len(r.Roots) == 0 {
		f.roots = "<none>"
		return
	}
	f.roots = r.Roots[0].URI + " (" + r.Roots[0].Name + ")"
}

// read decodes the next frame from stdin, skipping anything that is not
// JSON. It reports false once stdin is closed.
func (f *fake) read() (message, bool) {
	for f.sc.Scan() {
		var m message
		if err := json.Unmarshal(f.sc.Bytes(), &m); err != nil {
			continue
		}
		return m, true
	}
	return message{}, false
}

// next returns the oldest deferred client message, or reads a new one.
func (f *fake) next() (message, bool) {
	if len(f.queue) > 0 {
		m := f.queue[0]
		f.queue = f.queue[1:]
		return m, true
	}
	return f.read()
}

// await reads until the client's response to id arrives, deferring any
// client requests that overtake it — the client answers server requests
// on a goroutine of its own, so a tools/list can and does arrive first.
// It reads past the queue deliberately: the queue holds only the
// requests it deferred, never the response it is waiting for.
func (f *fake) await(id string) (message, bool) {
	for {
		m, ok := f.read()
		if !ok {
			return message{}, false
		}
		if m.Method == "" && string(m.ID) == id {
			return m, true
		}
		f.queue = append(f.queue, m)
	}
}

func (f *fake) result(id json.RawMessage, result any) {
	b, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	f.write(message{ID: id, Result: b})
}

func (f *fake) write(m message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(append(b, '\n'))
}

// safeBuf collects the server's stderr. exec copies into it from its own
// goroutine while tests read it, so every access is locked.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startFake starts the test binary as an MCP server and returns the
// connected client plus its captured stderr.
func startFake(t *testing.T, mode string) (*Client, *safeBuf) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var stderr safeBuf
	cfg := ServerConfig{
		Name:    "fake",
		Command: self,
		Env:     map[string]string{fakeEnv: mode},
		Root:    t.TempDir(),
	}
	c, err := Start(t.Context(), cfg, &stderr)
	if err != nil {
		t.Fatalf("start: %v (stderr: %s)", err, stderr.String())
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, &stderr
}

// waitFor polls cond for up to d, so tests never sleep a fixed amount
// waiting on the server's stderr.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
