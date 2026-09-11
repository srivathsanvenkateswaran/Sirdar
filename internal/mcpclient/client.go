package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// handshakeTimeout bounds initialize. A server that has not
	// answered by then is killed rather than left to hold the run open.
	handshakeTimeout = 30 * time.Second
	// requestTimeout is the default ceiling on tools/list and
	// tools/call. The caller's context shortens it but never extends it.
	requestTimeout = 120 * time.Second
	// shutdownGrace is how long Close waits after closing stdin before
	// killing the server's process group.
	shutdownGrace = 5 * time.Second
)

// Client is a connection to one MCP server subprocess.
//
// A single reader goroutine (started by Start) owns the server's stdout
// for the client's lifetime and routes each frame: responses go to the
// pending call that asked for them, keyed by request id; server-to-client
// requests are answered on a goroutine of their own so the reader never
// blocks on a write; notifications are dropped. Callers therefore hold
// c.mu only long enough to register a pending channel and write a line,
// never while waiting — which is what lets Close's "close stdin, wait,
// kill the group" guarantee hold even with a call in flight against a
// server that has stopped answering.
type Client struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	root string // workspace root reported to roots/list; may be empty

	mu      sync.Mutex
	next    int
	pending map[int]chan *message

	closeOnce sync.Once
	closed    chan struct{} // closed once no further responses can arrive

	stopOnce sync.Once
	stopErr  error

	info ServerInfo
}

// Start spawns the server described by cfg, performs the initialize
// handshake (bounded by handshakeTimeout, or ctx if it is shorter) and
// sends notifications/initialized.
//
// ctx bounds the handshake only: the server outlives it deliberately, so
// a caller can start servers under a short setup context and keep using
// them for the whole run. Close is the only thing that stops the server.
// Anything the server writes to its stderr is copied to stderr, which may
// be nil to discard it.
func Start(ctx context.Context, cfg ServerConfig, stderr io.Writer) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("mcp server %q: no command", cfg.Name)
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = childEnv(cfg.Env, cfg.BaseEnv)
	if cfg.Root != "" {
		cmd.Dir = cfg.Root
	}
	// Own process group, so Close can kill the whole subtree. A server
	// launched through npx/uvx or a shell wrapper leaves grandchildren
	// holding the inherited stdio pipes; killing only cmd.Process would
	// leave those running and cmd.Wait blocked on the pipes they hold.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp server %q: start: %w", cfg.Name, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)

	c := &Client{
		cmd:     cmd,
		in:      stdin,
		root:    cfg.Root,
		pending: make(map[int]chan *message),
		closed:  make(chan struct{}),
	}
	go c.readLoop(scanner)

	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := c.handshake(hctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp server %q: %w", cfg.Name, err)
	}
	return c, nil
}

// childEnv builds the server's environment: a deliberately small base
// (PATH, HOME, LANG, each only when base has it) with the config's own
// entries merged on top. Servers get what they need to find their
// interpreter and their home directory, and nothing else of Sirdar's
// environment — credentials included — unless .mcp.json asked for it.
//
// base is the session's child environment when the caller supplied one
// (ServerConfig.BaseEnv), so a credential internal/run strips does not
// come back through PATH's neighbours; nil means this process's.
func childEnv(extra map[string]string, base []string) []string {
	lookup := os.LookupEnv
	if base != nil {
		lookup = envLookup(base)
	}
	env := make([]string, 0, 3+len(extra))
	for _, k := range []string{"PATH", "HOME", "LANG"} {
		if _, ok := extra[k]; ok {
			continue // the config's value wins; added below
		}
		if v, ok := lookup(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// handshake runs initialize and, on success, sends the
// notifications/initialized that tells the server it may start issuing
// requests of its own.
func (c *Client) handshake(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"roots": map[string]any{"listChanged": false},
		},
		"clientInfo": map[string]string{"name": clientName, "version": clientVersion},
	}
	var res initializeResult
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	c.info = ServerInfo{
		Name:            res.ServerInfo.Name,
		Version:         res.ServerInfo.Version,
		ProtocolVersion: res.ProtocolVersion,
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized: %w", err)
	}
	return nil
}

// ServerInfo returns what the server said about itself during the
// handshake.
func (c *Client) ServerInfo() ServerInfo { return c.info }

// readLoop owns the server's stdout for the client's lifetime, routing
// each decoded frame and tolerating (skipping) any line that is not JSON
// — servers do occasionally print a banner to stdout. When stdout hits
// EOF it signals closed so calls still waiting stop waiting instead of
// hanging on a response that can never come.
func (c *Client) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] != '{' {
			continue // noise, or a Content-Length header Sirdar does not frame
		}
		var m message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go c.answer(&m) // server-to-client request
		case m.Method != "":
			// Notification. Sirdar subscribes to none of them, and
			// the spec requires unknown ones to be ignored.
		default:
			c.deliver(&m)
		}
	}
	c.signalClosed()
}

// deliver hands a response to whichever call is waiting on its id,
// dropping it if none is (a call that already timed out).
func (c *Client) deliver(m *message) {
	id, ok := intID(m.ID)
	if !ok {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- m
	}
}

// answer replies to a server-to-client request. roots/list gets the
// workspace root when one is configured and an empty list when it is
// not; everything else — sampling/createMessage, elicitation, anything a
// future revision adds — gets "method not found", which servers are
// required to handle. Answering rather than staying silent matters:
// several servers block their own startup on the first request.
func (c *Client) answer(m *message) {
	reply := message{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "roots/list":
		roots := []map[string]string{}
		if c.root != "" {
			roots = append(roots, map[string]string{
				"uri":  "file://" + c.root,
				"name": "workspace",
			})
		}
		b, err := json.Marshal(map[string]any{"roots": roots})
		if err != nil {
			return
		}
		reply.Result = b
	default:
		reply.Error = &rpcError{Code: codeMethodNotFound, Message: "method not supported: " + m.Method}
	}
	_ = c.writeMessage(&reply)
}

func (c *Client) signalClosed() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// writeMessage marshals and writes one frame, holding the lock only for
// the write itself.
func (c *Client) writeMessage(m *message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.in.Write(b)
	return err
}

// notify sends a notification (a request without an id, which gets no
// response).
func (c *Client) notify(method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.writeMessage(&message{Method: method, Params: b})
}

// call sends a request and waits for its response, for ctx to be done,
// or for the server to close stdout — whichever comes first. It never
// holds c.mu while waiting.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}

	ch := make(chan *message, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()

	m := &message{ID: json.RawMessage(fmt.Sprintf("%d", id)), Method: method, Params: raw}
	if err := c.writeMessage(m); err != nil {
		c.forget(id)
		return fmt.Errorf("write %s: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.closed:
		c.forget(id)
		return fmt.Errorf("%s: server closed", method)
	}
}

func (c *Client) forget(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// ListTools returns every tool the server exposes, following the
// nextCursor pagination until the server stops handing one back. A
// server that keeps returning the same cursor is cut off rather than
// paged forever.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var out []ToolInfo
	seen := map[string]bool{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page listToolsResult
		if err := c.call(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" || seen[page.NextCursor] {
			return out, nil
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

// CallTool invokes one tool and flattens its content items into a single
// string: text items verbatim, image items as "[image <mimeType>]", and
// embedded resources as their text when they carry any and
// "[resource <uri>]" when they do not.
//
// The returned bool is the server's own isError flag — a tool that
// failed on its own terms, which the loop feeds back to the model as
// tool output. A non-nil error means the call itself failed (transport,
// timeout, or a JSON-RPC error) and there is no output to show.
//
// The call is bounded by requestTimeout; ctx shortens that but cannot
// extend it.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	params := map[string]any{"name": name, "arguments": args}
	var res callToolResult
	if err := c.call(ctx, "tools/call", params, &res); err != nil {
		return "", false, err
	}

	var parts []string
	for _, item := range res.Content {
		switch item.Type {
		case "text":
			parts = append(parts, item.Text)
		case "image", "audio":
			mime := item.MimeType
			if mime == "" {
				mime = "application/octet-stream"
			}
			parts = append(parts, "["+item.Type+" "+mime+"]")
		case "resource":
			switch {
			case item.Resource == nil:
				parts = append(parts, "[resource]")
			case item.Resource.Text != "":
				parts = append(parts, item.Resource.Text)
			default:
				parts = append(parts, "[resource "+item.Resource.URI+"]")
			}
		default:
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		}
	}
	return strings.Join(parts, "\n"), res.IsError, nil
}

// Close shuts the server down: stdin is closed (the stdio transport's
// signal to exit), and a server that has not exited within shutdownGrace
// has its whole process group killed. It is safe to call more than once
// and returns nil when the server exited on its own or was killed by us;
// only an unexpected wait failure is reported.
func (c *Client) Close() error {
	c.stopOnce.Do(func() {
		_ = c.in.Close()

		done := make(chan error, 1)
		go func() { done <- c.cmd.Wait() }()

		select {
		case err := <-done:
			c.stopErr = exitErr(err)
		case <-time.After(shutdownGrace):
			c.killGroup()
			<-done // the kill is what makes this return
		}
		c.signalClosed()
	})
	return c.stopErr
}

// exitErr filters out the ordinary ways a stdio server ends — a clean
// exit, a non-zero status on stdin close, a signal — leaving only
// failures a caller could act on.
func exitErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil
	}
	return err
}

// killGroup SIGKILLs the server's process group (see the Setpgid comment
// in Start), falling back to the immediate child if the group is gone.
func (c *Client) killGroup() {
	if c.cmd.Process == nil {
		return
	}
	if pid := c.cmd.Process.Pid; pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
			return
		}
	}
	_ = c.cmd.Process.Kill()
}

// intID decodes a JSON-RPC id as an int. Sirdar only ever waits on ids
// it minted itself, which are always integers, so a string id here is a
// frame meant for somebody else.
func intID(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}
