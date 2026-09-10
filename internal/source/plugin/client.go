package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Client is a source.Tracker and source.Helpdesk backed by an adapter
// subprocess speaking the stdio protocol described in docs/adapters.md.
type Client struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner

	mu   sync.Mutex
	next int

	describeOnce sync.Once
	describe     Describe
	describeErr  error
}

// Start spawns command (parsed with shell-style double-quote handling) and
// returns a Client wired to its stdin/stdout. The adapter's stderr is
// copied to stderr for diagnostics.
func Start(ctx context.Context, command string, stderr io.Writer) (*Client, error) {
	argv := splitCommand(command)
	if len(argv) == 0 {
		return nil, fmt.Errorf("start adapter: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start adapter: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start adapter: %w", err)
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start adapter: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)

	return &Client{cmd: cmd, in: stdin, out: scanner}, nil
}

// splitCommand splits command into argv, honouring double-quoted segments
// (with backslash-escaped quotes inside them) the way a shell would, and
// expanding a leading "~/" in argv[0] to the user's home directory.
func splitCommand(command string) []string {
	var argv []string
	var cur strings.Builder
	inQuotes := false
	hasCur := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case ch == '\\' && inQuotes && i+1 < len(command) && command[i+1] == '"':
			cur.WriteByte('"')
			i++
			hasCur = true
		case ch == '"':
			inQuotes = !inQuotes
			hasCur = true
		case ch == ' ' && !inQuotes:
			if hasCur {
				argv = append(argv, cur.String())
				cur.Reset()
				hasCur = false
			}
		default:
			cur.WriteByte(ch)
			hasCur = true
		}
	}
	if hasCur {
		argv = append(argv, cur.String())
	}

	if len(argv) > 0 && strings.HasPrefix(argv[0], "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			argv[0] = filepath.Join(home, argv[0][2:])
		}
	}

	return argv
}

// call sends a request and waits for the matching response, tolerating
// (and skipping) any stdout lines that don't parse as a Response or whose
// ID doesn't match.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.next++
	req := Request{ID: c.next, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &source.Error{Code: source.Internal, Message: err.Error()}
		}
		req.Params = b
	}
	b, err := json.Marshal(req)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: err.Error()}
	}
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		return &source.Error{Code: source.Internal, Message: err.Error()}
	}

	for c.out.Scan() {
		var resp Response
		if err := json.Unmarshal(c.out.Bytes(), &resp); err != nil {
			continue // tolerate noise
		}
		if resp.ID != req.ID {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
	if err := c.out.Err(); err != nil {
		return &source.Error{Code: source.Internal, Message: err.Error()}
	}
	return &source.Error{Code: source.Internal, Message: "adapter closed stdout"}
}

// Describe calls the adapter's "describe" method once and caches the
// result for subsequent calls.
func (c *Client) Describe(ctx context.Context) (Describe, error) {
	c.describeOnce.Do(func() {
		c.describeErr = c.call(ctx, "describe", nil, &c.describe)
	})
	return c.describe, c.describeErr
}

func (c *Client) hasRole(ctx context.Context, role string) error {
	d, err := c.Describe(ctx)
	if err != nil {
		return err
	}
	for _, r := range d.Roles {
		if r == role {
			return nil
		}
	}
	return &source.Error{Code: source.Unsupported, Message: "adapter does not support role " + role}
}

// Get fetches a tracker ticket by key.
func (c *Client) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	var tt ticket.TrackerTicket
	if err := c.hasRole(ctx, "tracker"); err != nil {
		return tt, err
	}
	err := c.call(ctx, "tracker.get", map[string]string{"key": key}, &tt)
	return tt, err
}

// List fetches tracker tickets matching f.
func (c *Client) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	var tts []ticket.TrackerTicket
	if err := c.hasRole(ctx, "tracker"); err != nil {
		return tts, err
	}
	err := c.call(ctx, "tracker.list", f, &tts)
	return tts, err
}

// Threads fetches the conversation for a helpdesk ticket.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	var th ticket.Thread
	if err := c.hasRole(ctx, "helpdesk"); err != nil {
		return th, err
	}
	err := c.call(ctx, "helpdesk.threads", map[string]string{"id": id}, &th)
	return th, err
}

// Attachments downloads a helpdesk ticket's attachments into dir and
// returns their metadata.
func (c *Client) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	var atts []ticket.Attachment
	if err := c.hasRole(ctx, "helpdesk"); err != nil {
		return atts, err
	}
	err := c.call(ctx, "helpdesk.attachments", map[string]string{"id": id, "dir": dir}, &atts)
	return atts, err
}

// getHelpdesk fetches a helpdesk ticket by id. It backs the Helpdesk()
// view rather than being exported as Get directly on Client: Client
// already exposes Get for source.Tracker, and Go does not allow two
// methods of the same name with different signatures on one type.
func (c *Client) getHelpdesk(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	var ht ticket.HelpdeskTicket
	if err := c.hasRole(ctx, "helpdesk"); err != nil {
		return ht, err
	}
	err := c.call(ctx, "helpdesk.get", map[string]string{"id": id}, &ht)
	return ht, err
}

// Helpdesk returns a view of the client that satisfies source.Helpdesk.
// (Client itself satisfies source.Tracker directly; the two interfaces
// can't both be satisfied by one type because their Get methods collide
// by name with different signatures.)
func (c *Client) Helpdesk() source.Helpdesk { return helpdeskView{c} }

// helpdeskView adapts Client to source.Helpdesk.
type helpdeskView struct{ *Client }

func (h helpdeskView) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return h.Client.getHelpdesk(ctx, id)
}

var (
	_ source.Tracker  = (*Client)(nil)
	_ source.Helpdesk = helpdeskView{}
	_ source.Closer   = (*Client)(nil)
)

// Close sends a shutdown request, then waits up to 5s for the adapter to
// exit before killing it. It does not wait for a shutdown response: an
// adapter that never reads or replies (e.g. a stuck process) must still
// be killed on schedule.
func (c *Client) Close() error {
	c.mu.Lock()
	c.next++
	req := Request{ID: c.next, Method: "shutdown"}
	if b, err := json.Marshal(req); err == nil {
		_, _ = c.in.Write(append(b, '\n'))
	}
	c.mu.Unlock()
	_ = c.in.Close()

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
		return nil
	}
}
