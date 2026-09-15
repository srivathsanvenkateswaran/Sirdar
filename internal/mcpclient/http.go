package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// httpConfig is one remote MCP server: the endpoint and the headers the
// entry declared, already expanded. Header values are credentials, which
// is why nothing prints this struct.
type httpConfig struct {
	URL     string
	Headers map[string]string
}

// httpClient speaks the 2025-06-18 spec's streamable-HTTP transport: each
// JSON-RPC request is a POST, and the answer comes back either as one JSON
// object or as an SSE stream whose events carry the frames. Only what
// `sirdar mcp` needs is implemented — initialize, tools/list, tools/call —
// with no listening GET stream, so a server that only pushes
// notifications has nothing to push them down and none are expected.
type httpClient struct {
	name    string
	url     string
	headers map[string]string
	hc      *http.Client

	mu      sync.Mutex
	next    int
	session string

	info ServerInfo
}

// httpBodyLimit caps what is read from one response. A tools/list for a
// large server is tens of kilobytes; this is room for that and a bound on
// a server that streams forever.
const httpBodyLimit = 8 << 20

// startHTTP opens a session with a remote server: initialize, then
// notifications/initialized, bounded by handshakeTimeout or ctx, whichever
// is shorter.
func startHTTP(ctx context.Context, name string, cfg httpConfig) (*httpClient, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("mcp server %q: no url", name)
	}
	c := &httpClient{
		name:    name,
		url:     cfg.URL,
		headers: cfg.Headers,
		hc:      &http.Client{Timeout: requestTimeout},
	}

	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": clientName, "version": clientVersion},
	}
	var res initializeResult
	if err := c.call(hctx, "initialize", params, &res); err != nil {
		return nil, fmt.Errorf("mcp server %q: initialize: %w", name, err)
	}
	c.info = ServerInfo{
		Name:            res.ServerInfo.Name,
		Version:         res.ServerInfo.Version,
		ProtocolVersion: res.ProtocolVersion,
	}
	if err := c.call(hctx, "notifications/initialized", map[string]any{}, nil); err != nil {
		return nil, fmt.Errorf("mcp server %q: initialized: %w", name, err)
	}
	return c, nil
}

func (c *httpClient) ServerInfo() ServerInfo { return c.info }

// ListTools pages through tools/list exactly as the stdio client does.
func (c *httpClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
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

// CallTool invokes one tool and flattens its content the way the stdio
// client does, so a caller cannot tell the two transports apart.
func (c *httpClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var res callToolResult
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", false, err
	}
	return flatten(res.Content), res.IsError, nil
}

// Close ends the session the way the spec asks, with a DELETE naming it.
// A server that never issued a session id, or that refuses the DELETE, is
// not an error: there is nothing left for a caller to do about it.
func (c *httpClient) Close() error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return nil
	}
	c.setHeaders(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.Body.Close()
}

// call sends one request — or, when result is nil, one notification — and
// decodes the response into result.
func (c *httpClient) call(ctx context.Context, method string, params any, result any) error {
	m := message{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = b
	}
	notification := strings.HasPrefix(method, "notifications/")
	var wantID string
	if !notification {
		c.mu.Lock()
		c.next++
		id := c.next
		c.mu.Unlock()
		wantID = fmt.Sprintf("%d", id)
		m.ID = json.RawMessage(wantID)
	}

	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.setHeaders(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		c.mu.Lock()
		c.session = id
		c.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(method, resp)
	}
	if notification || resp.StatusCode == http.StatusAccepted {
		return nil
	}

	frame, err := readFrame(resp, wantID)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if frame.Error != nil {
		return frame.Error
	}
	if result != nil && len(frame.Result) > 0 {
		return json.Unmarshal(frame.Result, result)
	}
	return nil
}

// setHeaders adds the protocol headers and the entry's own — an
// Authorization bearer, a vendor api-key — to a request.
func (c *httpClient) setHeaders(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

// statusError turns a non-2xx response into the error an operator reads.
//
// 401 and 403 are the two an operator can act on and the two whose body is
// most likely to quote back what was sent, so they get a fixed sentence and
// nothing of the response at all. Every other status carries the server's
// own message, capped — and Connect wraps this client so the entry's
// credentials are taken out of whatever it says.
func statusError(method string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: 401 from the token, check its scope", method)
	case http.StatusForbidden:
		return fmt.Errorf("%s: 403 from the token, check its scope", method)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	detail := strings.TrimSpace(string(b))
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	if len(detail) > 200 {
		detail = detail[:200] + "…"
	}
	if detail == "" {
		return fmt.Errorf("%s: http %d", method, resp.StatusCode)
	}
	return fmt.Errorf("%s: http %d: %s", method, resp.StatusCode, detail)
}

// readFrame returns the response frame for wantID, from either shape the
// transport allows: a single JSON object, or an SSE stream whose data
// lines carry the frames.
func readFrame(resp *http.Response, wantID string) (*message, error) {
	body := io.LimitReader(resp.Body, httpBodyLimit)
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		var m message
		if err := json.NewDecoder(body).Decode(&m); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &m, nil
	}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" || data[0] != '{' {
			continue
		}
		var m message
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			continue
		}
		// Notifications and server-to-client requests share the stream
		// with the answer; only the frame this request asked for ends it.
		if m.Method != "" || string(m.ID) != wantID {
			continue
		}
		return &m, nil
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("the stream ended without a response")
}
