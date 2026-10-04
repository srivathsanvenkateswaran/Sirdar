// Package slack reads one Slack message, or the thread it belongs to, over
// the Slack Web API. It is read-only and deliberately small: two methods,
// conversations.history for a single message and conversations.replies for
// a thread, plus auth.test for `sirdar doctor`. There is no SDK behind it.
//
// It exists so a support engineer can hand Sirdar the Slack link a
// colleague posted instead of copying the ticket number out of it: the
// intake resolver reads the message and its thread, finds the tracker key
// or helpdesk number in the text, and carries the thread into the bundle.
//
// The token is a credential reference resolved by the caller and held only
// in memory. It is sent as a bearer header to slack.com and nowhere else:
// every request URL is built from a fixed base, and a redirect off slack.com
// is refused rather than followed.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
)

// apiBase is the only place this client sends a request. It is a constant,
// not configuration: Slack's Web API lives on one host for every workspace,
// and a configurable base would be a way to send the token somewhere else.
const apiBase = "https://slack.com/api"

// MaxMessages is the most messages one read returns. A support thread is a
// handful of messages; past fifty the rest is chatter, and the cap keeps a
// pasted link to a busy channel from becoming a bundle the size of a log.
const MaxMessages = 50

// maxBody caps one API response.
const maxBody = 4 << 20

// Client reads Slack messages with one token.
type Client struct {
	// Token is the resolved user (xoxp-) or bot (xoxb-) token.
	Token string
	// HTTP is the client requests are sent with; nil means a 30-second
	// default. Its redirect policy is replaced on every call with one
	// pinned to slack.com.
	HTTP *http.Client
}

// New returns a Client for token.
func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Link is a parsed Slack permalink: the channel, the message's own
// timestamp, and the thread it sits in when the link says so.
type Link struct {
	URL      string
	Team     string
	Channel  string
	TS       string
	ThreadTS string
}

// permalink matches https://<team>.slack.com/archives/<channel>/p<ts>. The
// team may have more than one label (an Enterprise Grid org's
// "acme.enterprise.slack.com"); the host must end in .slack.com on a dot
// boundary, so "slack.com.example.org" is not a Slack link.
var permalink = regexp.MustCompile(`https://([a-z0-9][a-z0-9-]*(?:\.[a-z0-9][a-z0-9-]*)*)\.slack\.com/archives/([A-Z0-9]{6,})/p([0-9]{16})(\?[^\s<>"')\]]*)?`)

// FindLink returns the first Slack permalink in text, and whether there was
// one.
func FindLink(text string) (Link, bool) {
	m := permalink.FindStringSubmatchIndex(text)
	if m == nil {
		return Link{}, false
	}
	raw := text[m[0]:m[1]]
	sub := func(i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return text[m[2*i]:m[2*i+1]]
	}
	l := Link{URL: strings.TrimRight(raw, ".,;:"), Team: sub(1), Channel: sub(2), TS: tsOf(sub(3))}
	if q := sub(4); q != "" {
		if v, err := url.ParseQuery(strings.TrimPrefix(strings.TrimRight(q, ".,;:"), "?")); err == nil {
			if ts := v.Get("thread_ts"); validTS(ts) {
				l.ThreadTS = ts
			}
		}
	}
	return l, true
}

// tsOf turns a permalink's p1712345678901234 into the API's
// "1712345678.901234": the last six digits are the microseconds.
func tsOf(digits string) string {
	if len(digits) <= 6 {
		return digits
	}
	return digits[:len(digits)-6] + "." + digits[len(digits)-6:]
}

var tsShape = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)

func validTS(ts string) bool { return tsShape.MatchString(ts) }

// Message is one Slack message as the bundle and the resolver read it.
type Message struct {
	TS     string
	At     time.Time
	Author string
	Text   string
	// Titles are the titles of the message's attachments and files, which
	// is where an unfurled helpdesk or tracker link puts the ticket's name.
	Titles []string
}

// Thread is what one read returns: the message the link points at and, when
// it is part of a thread, the thread around it, oldest first.
type Thread struct {
	Link     Link
	Messages []Message
	// Truncated is set when the thread had more than MaxMessages.
	Truncated bool
	// IsThread is set when the messages are a thread rather than the one
	// message.
	IsThread bool
	// Refs are ticket references a reader found beside the texts: the
	// MCP path's model lists the keys and helpdesk numbers it saw. The
	// Web API path leaves it empty; the resolver scans the texts either way.
	Refs []string
}

// Texts are every message's text and attachment titles, the linked message
// first, for a resolver scanning them for a ticket reference.
func (t Thread) Texts() []string {
	var first, rest []string
	for _, m := range t.Messages {
		parts := append([]string{m.Text}, m.Titles...)
		if m.TS == t.Link.TS {
			first = append(first, parts...)
		} else {
			rest = append(rest, parts...)
		}
	}
	return append(first, rest...)
}

// Read fetches the message a link points at and its thread: the replies of
// thread_ts when the link names one, else the one message by its timestamp
// and, when it started a thread, that thread.
func (c *Client) Read(ctx context.Context, l Link) (Thread, error) {
	out := Thread{Link: l}
	if l.Channel == "" || !validTS(l.TS) {
		return out, &source.Error{Code: source.Internal, Message: "slack: the link has no channel or message timestamp"}
	}
	if l.ThreadTS != "" {
		msgs, more, err := c.replies(ctx, l.Channel, l.ThreadTS)
		if err != nil {
			return out, err
		}
		out.Messages, out.Truncated, out.IsThread = msgs, more, true
		return out, nil
	}

	q := url.Values{}
	q.Set("channel", l.Channel)
	q.Set("latest", l.TS)
	q.Set("inclusive", "true")
	q.Set("limit", "1")
	var hist historyResponse
	if err := c.call(ctx, "conversations.history", q, &hist); err != nil {
		return out, err
	}
	if len(hist.Messages) == 0 || hist.Messages[0].TS != l.TS {
		return out, &source.Error{Code: source.NotFound, Message: "slack: the message is gone or this token cannot see it"}
	}
	head := hist.Messages[0]
	if head.ReplyCount > 0 || (head.ThreadTS != "" && head.ThreadTS != head.TS) {
		thread := head.ThreadTS
		if thread == "" {
			thread = head.TS
		}
		msgs, more, err := c.replies(ctx, l.Channel, thread)
		if err != nil {
			return out, err
		}
		out.Messages, out.Truncated, out.IsThread = msgs, more, true
		return out, nil
	}
	out.Messages = []Message{head.message()}
	return out, nil
}

// replies reads a thread, capped at MaxMessages.
func (c *Client) replies(ctx context.Context, channel, ts string) ([]Message, bool, error) {
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("ts", ts)
	q.Set("limit", strconv.Itoa(MaxMessages))
	var resp historyResponse
	if err := c.call(ctx, "conversations.replies", q, &resp); err != nil {
		return nil, false, err
	}
	more := resp.HasMore
	raw := resp.Messages
	if len(raw) > MaxMessages {
		raw, more = raw[:MaxMessages], true
	}
	msgs := make([]Message, 0, len(raw))
	for _, m := range raw {
		msgs = append(msgs, m.message())
	}
	return msgs, more, nil
}

// Who is the account a token belongs to, for the doctor row.
type Who struct {
	User string
	Team string
}

// AuthTest checks the token and says whose it is.
func (c *Client) AuthTest(ctx context.Context) (Who, error) {
	var resp struct {
		envelope
		User string `json:"user"`
		Team string `json:"team"`
	}
	if err := c.call(ctx, "auth.test", nil, &resp); err != nil {
		return Who{}, err
	}
	return Who{User: resp.User, Team: resp.Team}, nil
}

// envelope is every Web API response's ok/error pair.
type envelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (e envelope) envelopeOf() envelope { return e }

type enveloped interface{ envelopeOf() envelope }

type historyResponse struct {
	envelope
	Messages []apiMessage `json:"messages"`
	HasMore  bool         `json:"has_more"`
}

type apiMessage struct {
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	ReplyCount  int    `json:"reply_count"`
	User        string `json:"user"`
	Username    string `json:"username"`
	Text        string `json:"text"`
	UserProfile *struct {
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
	} `json:"user_profile"`
	BotProfile *struct {
		Name string `json:"name"`
	} `json:"bot_profile"`
	Attachments []struct {
		Title    string `json:"title"`
		Fallback string `json:"fallback"`
	} `json:"attachments"`
	Files []struct {
		Title string `json:"title"`
		Name  string `json:"name"`
	} `json:"files"`
}

func (m apiMessage) message() Message {
	out := Message{TS: m.TS, At: timeOf(m.TS), Author: m.author(), Text: m.Text}
	for _, a := range m.Attachments {
		if t := strings.TrimSpace(a.Title); t != "" {
			out.Titles = append(out.Titles, t)
		} else if f := strings.TrimSpace(a.Fallback); f != "" {
			out.Titles = append(out.Titles, f)
		}
	}
	for _, f := range m.Files {
		if t := strings.TrimSpace(f.Title); t != "" {
			out.Titles = append(out.Titles, t)
		} else if n := strings.TrimSpace(f.Name); n != "" {
			out.Titles = append(out.Titles, n)
		}
	}
	return out
}

// author is the best name the message carries without a users.info call:
// the profile Slack embeds, the username a bot or integration posts under,
// and the user id as the last resort.
func (m apiMessage) author() string {
	if p := m.UserProfile; p != nil {
		if p.DisplayName != "" {
			return p.DisplayName
		}
		if p.RealName != "" {
			return p.RealName
		}
	}
	if m.Username != "" {
		return m.Username
	}
	if m.BotProfile != nil && m.BotProfile.Name != "" {
		return m.BotProfile.Name
	}
	if m.User != "" {
		return m.User
	}
	return "unknown"
}

// timeOf reads a message timestamp as a time.
func timeOf(ts string) time.Time {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	us, _ := strconv.ParseInt(frac, 10, 64)
	return time.Unix(s, us*1000).UTC()
}

// trust pins every request and every redirect hop to slack.com.
var trust = func() *httpx.Trust {
	t, err := httpx.NewTrust(apiBase)
	if err != nil {
		panic(err)
	}
	return t
}()

// call issues one Web API GET and decodes it into out, which must embed
// envelope. A response with ok:false is an error carrying Slack's own error
// code, which is the part a person can act on ("missing_scope",
// "channel_not_found").
func (c *Client) call(ctx context.Context, method string, q url.Values, out enveloped) error {
	if strings.TrimSpace(c.Token) == "" {
		return &source.Error{Code: source.Auth, Message: "slack: no token"}
	}
	u := apiBase + "/" + method
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if fetch, _, reason := trust.CheckRaw(u); !fetch {
		return &source.Error{Code: source.Internal, Message: "slack: refusing to call " + httpx.HostOf(u) + ": " + reason}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: "slack: " + method + ": " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	base := c.HTTP
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpx.Client(base, trust, 0).Do(req)
	if err != nil {
		if host, ok := httpx.RedirectHost(err); ok {
			return &source.Error{Code: source.Internal, Message: "slack: " + method + ": redirect to untrusted host " + host}
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return &source.Error{Code: source.Internal, Message: "slack: " + method + ": " + err.Error()}
	}
	defer resp.Body.Close()
	body, err := httpx.ReadLimited(resp.Body, maxBody)
	if err != nil {
		return &source.Error{Code: source.Internal, Message: "slack: " + method + ": " + err.Error()}
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: "slack: " + method + ": rate limited"}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("slack: %s: status %d", method, resp.StatusCode)}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("slack: %s: status %d", method, resp.StatusCode)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: "slack: " + method + ": decode: " + err.Error()}
	}
	if env := out.envelopeOf(); !env.OK {
		return apiError(method, env.Error)
	}
	return nil
}

// apiError maps Slack's error codes onto the source error classes.
func apiError(method, code string) *source.Error {
	if code == "" {
		code = "unknown_error"
	}
	msg := "slack: " + method + ": " + code
	switch code {
	case "not_authed", "invalid_auth", "account_inactive", "token_revoked", "token_expired", "missing_scope", "not_allowed_token_type":
		return &source.Error{Code: source.Auth, Message: msg}
	case "channel_not_found", "thread_not_found", "message_not_found", "not_in_channel":
		return &source.Error{Code: source.NotFound, Message: msg}
	case "ratelimited":
		return &source.Error{Code: source.RateLimited, Message: msg}
	}
	return &source.Error{Code: source.Internal, Message: msg}
}
