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

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Client is a source.Tracker and source.Helpdesk backed by an adapter
// subprocess speaking the stdio protocol described in docs/adapters.md.
//
// A dedicated goroutine (started by Start) owns reading the adapter's
// stdout and dispatches each decoded Response to the pending call that
// requested it, keyed by ID. call therefore never blocks holding c.mu:
// it holds the lock only long enough to write the request, then waits on
// its own response channel. This keeps Close's "shutdown, wait 5s, kill"
// guarantee intact even while a call is in flight and the adapter has
// stopped responding — Close never contends with the reader or with an
// in-flight call for the lock.
type Client struct {
	cmd *exec.Cmd
	in  io.WriteCloser

	mu      sync.Mutex
	next    int
	pending map[int]chan Response

	closeOnce sync.Once
	closed    chan struct{} // closed once no further responses will ever arrive

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
	// Put the adapter in its own process group so Close can kill the
	// whole subtree, not just the immediate child. An adapter that is
	// (or spawns) a shell wrapper leaves grandchildren holding the
	// inherited stdio pipes open; killing only cmd.Process would leave
	// those descendants running and cmd.Wait blocked on the pipes they
	// still hold, well past the 5s deadline.
	procgroup.Setup(cmd)
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

	c := &Client{
		cmd:     cmd,
		in:      stdin,
		pending: make(map[int]chan Response),
		closed:  make(chan struct{}),
	}
	go c.readLoop(scanner)
	return c, nil
}

// readLoop owns the adapter's stdout for the client's lifetime. It
// decodes each line as a Response and delivers it to the pending call
// waiting on that ID, tolerating (skipping) any line that doesn't parse
// or whose ID has no waiter. When the adapter closes stdout (EOF) or the
// scanner otherwise gives up, it signals closed so any calls still
// waiting on a response stop waiting instead of blocking forever.
func (c *Client) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue // tolerate noise
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
	c.signalClosed()
}

func (c *Client) signalClosed() {
	c.closeOnce.Do(func() { close(c.closed) })
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

	// There is no shell between here and the adapter, so nothing else
	// will expand a "~/" — not in argv[0], and not in an argument such as
	// "--token-cmd-file ~/.sirdar/janus-token.sh", which the adapter
	// would otherwise be handed as a literal path that cannot exist.
	if home, err := os.UserHomeDir(); err == nil {
		for i, a := range argv {
			if strings.HasPrefix(a, "~/") {
				argv[i] = filepath.Join(home, a[2:])
			}
		}
	}

	return argv
}

// call sends a request and waits for the matching response (delivered by
// readLoop), for ctx to be cancelled, or for the client to be closed —
// whichever comes first. It holds c.mu only while registering the
// pending response channel and writing the request, never while waiting,
// so a slow or hung adapter can't block Close or other calls.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	req := Request{Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &source.Error{Code: source.Internal, Message: err.Error()}
		}
		req.Params = b
	}

	ch := make(chan Response, 1)
	c.mu.Lock()
	c.next++
	req.ID = c.next
	c.pending[req.ID] = ch
	b, err := json.Marshal(req)
	if err != nil {
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return &source.Error{Code: source.Internal, Message: err.Error()}
	}
	_, werr := c.in.Write(append(b, '\n'))
	c.mu.Unlock()
	if werr != nil {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return &source.Error{Code: source.Internal, Message: werr.Error()}
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
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return &source.Error{Code: source.Internal, Message: ctx.Err().Error()}
	case <-c.closed:
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return &source.Error{Code: source.Internal, Message: "adapter closed"}
	}
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

// WarningsFor implements source.Warner. The adapter protocol has no channel
// for per-item warnings today — an adapter reports partial failures on its
// own stderr, which Sirdar copies through — so there is never anything to
// return for any ticket id. The method exists so every source Sirdar ships
// answers the same interface, and so adding a warnings field to the
// protocol later is a change to this one method.
func (c *Client) WarningsFor(id string) []string { return nil }

var (
	_ source.Tracker  = (*Client)(nil)
	_ source.Helpdesk = helpdeskView{}
	_ source.Closer   = (*Client)(nil)
	_ source.Warner   = (*Client)(nil)
	_ source.Warner   = helpdeskView{}
)

// Close sends a shutdown request, then waits up to 5s for the adapter to
// exit before killing it. It does not wait for a shutdown response, and
// never blocks on the lock an in-flight call might be holding while
// writing: it takes c.mu only for the instant it needs to write the
// shutdown request, exactly like call does. Once the adapter has exited
// (or been killed), it signals closed so any call still waiting on a
// response — one the adapter will now never send — returns an error
// instead of hanging forever.
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

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		c.killGroup()
		err = <-done
	}
	c.signalClosed()
	return err
}

// killGroup sends SIGKILL to the adapter's whole process group (see the
// procgroup.Setup call in Start), falling back to killing just cmd.Process
// if the group is somehow gone already.
func (c *Client) killGroup() {
	_ = procgroup.Kill(c.cmd)
}
