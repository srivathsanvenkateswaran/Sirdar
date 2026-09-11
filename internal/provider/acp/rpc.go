package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
)

// callTimeout bounds a short request/response exchange — initialize,
// session/new, session/load — so an agent that never answers cannot hang
// the triage loop. session/prompt is not one of these: it stays open for
// the whole turn and is sent with callLong instead.
const callTimeout = 2 * time.Minute

// errConnClosed is returned by call/notify once the reader loop has stopped.
var errConnClosed = errors.New("acp: agent connection closed")

// JSON-RPC error codes this client sends back to an agent. ACP defines no
// codes of its own for a client refusing a capability it never advertised,
// and -32601 is what "the client does not implement this method" means.
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("acp: rpc error %d: %s", e.Code, e.Message)
}

// wireMessage covers all four JSON-RPC shapes on the stdio channel:
// request (id+method), notification (method only), response (id+result) and
// error response (id+error).
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// conn is a line-delimited JSON-RPC 2.0 connection to an ACP agent. One
// goroutine (run) owns the reader; writes are serialised by wmu, so any
// goroutine — including a request handler answering fs/read_text_file —
// may write without deadlocking the reader.
type conn struct {
	r io.Reader
	w io.WriteCloser

	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan wireMessage
	closed  bool
	readErr error

	// onNotify handles agent notifications (method, no id).
	onNotify func(method string, params json.RawMessage)
	// onRequest handles agent-to-client requests (method and id); the
	// handler is expected to call reply or replyError.
	onRequest func(id json.RawMessage, method string, params json.RawMessage)

	done chan struct{}
}

func newConn(r io.Reader, w io.WriteCloser) *conn {
	return &conn{
		r:       r,
		w:       w,
		pending: make(map[int64]chan wireMessage),
		done:    make(chan struct{}),
	}
}

// run reads the agent's stdout until EOF, routing every line. It is meant
// to be started in its own goroutine and returns once the stream ends.
func (c *conn) run() {
	sc := bufio.NewScanner(c.r)
	// An ACP tool_call_update can carry a whole file's contents as a diff,
	// so one line is allowed to be large.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg wireMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// A non-JSON line is noise from the CLI, not a protocol error:
			// several ACP agents print a banner before they start talking.
			continue
		}
		switch {
		case msg.Method != "" && hasID(msg.ID):
			if c.onRequest != nil {
				c.onRequest(msg.ID, msg.Method, msg.Params)
			}
		case msg.Method != "":
			if c.onNotify != nil {
				c.onNotify(msg.Method, msg.Params)
			}
		case hasID(msg.ID):
			c.deliver(msg)
		}
	}
	c.close(sc.Err())
}

// deliver hands a response to the goroutine waiting on its id.
func (c *conn) deliver(msg wireMessage) {
	id, ok := parseID(msg.ID)
	if !ok {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- msg
	}
}

// close marks the connection dead and unblocks every waiter.
func (c *conn) close(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if err != nil {
		c.readErr = err
	}
	pending := c.pending
	c.pending = make(map[int64]chan wireMessage)
	c.mu.Unlock()

	close(c.done)
	for id, ch := range pending {
		ch <- wireMessage{
			ID:    json.RawMessage(strconv.FormatInt(id, 10)),
			Error: &rpcError{Message: "connection closed"},
		}
	}
}

// call sends a request and blocks until the matching response arrives, or
// callTimeout passes.
func (c *conn) call(method string, params any) (json.RawMessage, error) {
	return c.send(method, params, callTimeout)
}

// callLong sends a request that has no deadline of its own. session/prompt
// is open for as long as the agent takes to finish its turn, which is what
// the run's wall-clock budget and Cancel are for; a two-minute timeout here
// would abandon every investigation that took longer than that while the
// agent was still working.
func (c *conn) callLong(method string, params any) (json.RawMessage, error) {
	return c.send(method, params, 0)
}

func (c *conn) send(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	body, err := encodeParams(params)
	if err != nil {
		return nil, err
	}

	ch := make(chan wireMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errConnClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, body)
	if err := c.writeLine(line); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, msg.Error)
		}
		return msg.Result, nil
	case <-deadline:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("acp: %s timed out after %s", method, timeout)
	}
}

// notify sends a message that expects no response. session/cancel is one.
func (c *conn) notify(method string, params any) error {
	body, err := encodeParams(params)
	if err != nil {
		return err
	}
	return c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":%s}`, method, body))
}

// reply answers an agent-to-client request, echoing its id verbatim.
func (c *conn) reply(id json.RawMessage, result any) error {
	body, err := encodeParams(result)
	if err != nil {
		return err
	}
	return c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, bytes.TrimSpace(id), body))
}

// replyError refuses an agent-to-client request. Every refusal Sirdar sends
// is a capability it declined at initialize (writing files, terminals) or a
// path outside the workspace, so the agent is told plainly rather than
// being left waiting.
func (c *conn) replyError(id json.RawMessage, code int, message string) error {
	body, err := json.Marshal(rpcError{Code: code, Message: message})
	if err != nil {
		return err
	}
	return c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":%s}`, bytes.TrimSpace(id), body))
}

func (c *conn) writeLine(line string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errConnClosed
	}
	if _, err := io.WriteString(c.w, line+"\n"); err != nil {
		return err
	}
	return nil
}

// closeWrite closes the agent's stdin, which is how an ACP agent is asked
// to shut down: the protocol has no exit method.
func (c *conn) closeWrite() error { return c.w.Close() }

func encodeParams(params any) (json.RawMessage, error) {
	if params == nil {
		return json.RawMessage("{}"), nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		if len(bytes.TrimSpace(raw)) == 0 {
			return json.RawMessage("{}"), nil
		}
		return raw, nil
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// parseID reads a response id, which JSON-RPC allows to be a number or a
// string. Sirdar only ever sends numbers, but an agent that echoes them
// quoted must not wedge the call waiting on that id.
func parseID(raw json.RawMessage) (int64, bool) {
	trimmed := string(bytes.TrimSpace(raw))
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return id, true
	}
	var quoted string
	if err := json.Unmarshal([]byte(trimmed), &quoted); err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(quoted), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func hasID(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// ------------------------------------------------------------ process group

// setpgid puts the agent in a process group of its own, so killGroup can
// take down whatever it started. Several ACP agents are npx wrappers around
// a second process — `npx @agentclientprotocol/claude-agent-acp` is node
// spawning node — and killing only the wrapper leaves the real agent
// holding the workspace and the API session open.
//
// The platform halves live in internal/procgroup, which is also what keeps
// this file building for GOOS=windows, where there is no addressable
// process group and syscall.Kill does not exist.
func setpgid(cmd *exec.Cmd) {
	procgroup.Setup(cmd)
}

// killGroup SIGKILLs the whole process group the agent was started in (see
// setpgid), falling back to the process alone when the group kill is
// refused — which is what happens when the child has already reaped.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = procgroup.Kill(cmd)
}
