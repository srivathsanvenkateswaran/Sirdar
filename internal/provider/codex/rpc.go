package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// callTimeout bounds a single request/response exchange so a wedged
// app-server cannot hang the triage loop forever.
const callTimeout = 2 * time.Minute

// errConnClosed is returned by call/notify once the reader loop has stopped.
var errConnClosed = errors.New("codex: app-server connection closed")

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("codex: rpc error %d: %s", e.Code, e.Message)
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

// conn is a line-delimited JSON-RPC 2.0 connection to `codex app-server`.
// One goroutine (run) owns the reader; writes are serialised by wmu, so any
// goroutine — including a notification handler — may reply without deadlock.
type conn struct {
	r io.Reader
	w io.WriteCloser

	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan wireMessage
	closed  bool
	readErr error

	// onNotify handles server notifications (method, no id).
	onNotify func(method string, params json.RawMessage)
	// onRequest handles server-to-client requests (method and id); the
	// handler is expected to call reply.
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

// run reads the server's stdout until EOF, routing every line. It is meant to
// be started in its own goroutine and returns once the stream ends.
func (c *conn) run() {
	sc := bufio.NewScanner(c.r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg wireMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// A non-JSON line is noise from the CLI, not a protocol error.
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
	id, err := strconv.ParseInt(string(bytes.TrimSpace(msg.ID)), 10, 64)
	if err != nil {
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
		ch <- wireMessage{ID: json.RawMessage(strconv.FormatInt(id, 10)), Error: &rpcError{Message: "connection closed"}}
	}
}

// call sends a request and blocks until the matching response arrives.
func (c *conn) call(method string, params any) (json.RawMessage, error) {
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

	timer := time.NewTimer(callTimeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, msg.Error)
		}
		return msg.Result, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("codex: %s timed out after %s", method, callTimeout)
	}
}

// notify sends a request that expects no response.
func (c *conn) notify(method string, params any) error {
	body, err := encodeParams(params)
	if err != nil {
		return err
	}
	return c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":%s}`, method, body))
}

// reply answers a server-to-client request, echoing its id verbatim.
func (c *conn) reply(id json.RawMessage, result any) error {
	body, err := encodeParams(result)
	if err != nil {
		return err
	}
	return c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, bytes.TrimSpace(id), body))
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

// closeWrite closes the server's stdin, which is how a Codex app-server is
// asked to shut down.
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

func hasID(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
