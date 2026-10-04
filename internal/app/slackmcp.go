package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
)

// slackMCPServer is the name the operator's Slack MCP server must have in
// the Claude CLI's user scope, and in mcp.userServers, for a Slack link to
// be read through it.
const slackMCPServer = "slack"

// slackMCPReadTools are the only tools the reading session may call.
var slackMCPReadTools = []string{"mcp__slack__slack_read_*"}

// slackMCPMaxTurns is the reading session's ceiling. Three would be a tool
// call and an answer with a turn to spare, but the CLI loads an MCP
// server's tools on demand (a ToolSearch turn before the first call), which
// a verification run against Slack's own server showed; four leaves room
// for that and nothing else.
const slackMCPMaxTurns = 4

// slackMCPCacheTTL is how long a thread read through MCP is kept in this
// process. Resolving a link and then starting the run on it read the same
// thread twice; the second read is a cache hit rather than another model
// call. Half an hour covers the gap and is short enough that a thread that
// grew since is read again.
const slackMCPCacheTTL = 30 * time.Minute

// slackMCPSchema is the reading's shape: the messages, as written, and the
// ticket references the model saw in them.
var slackMCPSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["messages", "refs"],
  "properties": {
    "messages": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["author", "time", "text"],
        "properties": {
          "author": {"type": "string"},
          "time": {"type": "string", "description": "When it was posted: RFC 3339, or the Slack ts as given."},
          "text": {"type": "string", "description": "The message text exactly as written, with attachment and file titles appended on their own lines."}
        }
      }
    },
    "refs": {"type": "array", "items": {"type": "string"}, "description": "Every tracker key (like SBX-1) or helpdesk number (like #28310) the messages name, linked message first."}
  }
}`)

// slackMCPReader reads a Slack permalink through the operator's own Slack
// MCP server: one short Claude Code session that sees that server alone,
// may call only its read tools, and answers with the messages and the
// references in them. It is what a Slack link gets in a workspace with no
// sources.slack token but `slack` in mcp.userServers.
type slackMCPReader struct {
	prov   provider.Provider
	cfg    *config.Config
	server provider.UserMCPServer
}

// ViaMCP marks the reader for the intake chip, which names the hop
// "Slack (via MCP)".
func (r *slackMCPReader) ViaMCP() bool { return true }

var slackMCPCache = struct {
	sync.Mutex
	m map[string]slackMCPCached
}{m: map[string]slackMCPCached{}}

type slackMCPCached struct {
	thread slack.Thread
	at     time.Time
}

// SlackMCPPrompt is what the reading session is asked.
func SlackMCPPrompt(l slack.Link) string {
	var b strings.Builder
	b.WriteString("Read one Slack message, and the thread it belongs to, with the Slack MCP tools, and answer only with the JSON object the schema describes.\n\n")
	fmt.Fprintf(&b, "- Link: %s\n- Channel: %s\n- Message ts: %s\n", l.URL, l.Channel, tsOf(l.TS))
	if l.ThreadTS != "" {
		fmt.Fprintf(&b, "- Thread ts: %s\n", l.ThreadTS)
	}
	b.WriteString("\nCall mcp__slack__slack_read_thread with the channel and the thread ts (the message ts when there is no thread ts). ")
	b.WriteString("If the message is not in a thread, call mcp__slack__slack_read_channel and keep only that message. ")
	b.WriteString("Call no other tool, and nothing that writes.\n\n")
	b.WriteString("- messages: every message read, the linked one first, each with its author, time and text exactly as written. Do not translate or summarise.\n")
	b.WriteString("- refs: every tracker key (letters, a dash, digits, like SBX-1) and helpdesk number (#28310) the messages name, linked message first; [] when there are none.\n")
	return b.String()
}

// tsOf turns a permalink's p-timestamp into the dotted form the Slack API
// and its MCP tools take: 1712345678901234 → 1712345678.901234.
func tsOf(ts string) string {
	if strings.Contains(ts, ".") || len(ts) <= 6 {
		return ts
	}
	return ts[:len(ts)-6] + "." + ts[len(ts)-6:]
}

// Read answers the thread a link points at, from the process cache when it
// was read in the last half hour.
func (r *slackMCPReader) Read(ctx context.Context, l slack.Link) (slack.Thread, error) {
	slackMCPCache.Lock()
	if c, ok := slackMCPCache.m[l.URL]; ok && time.Since(c.at) < slackMCPCacheTTL {
		slackMCPCache.Unlock()
		return c.thread, nil
	}
	slackMCPCache.Unlock()

	th, err := r.read(ctx, l)
	if err != nil {
		return th, err
	}
	slackMCPCache.Lock()
	slackMCPCache.m[l.URL] = slackMCPCached{thread: th, at: time.Now()}
	slackMCPCache.Unlock()
	return th, nil
}

func (r *slackMCPReader) read(ctx context.Context, l slack.Link) (slack.Thread, error) {
	out := slack.Thread{Link: l}
	binary := ""
	if p := r.cfg.Providers.Claude.Path; p != "" {
		binary = r.cfg.ExpandPath(p)
	}
	sess, err := r.prov.Start(ctx, provider.SessionSpec{
		Cwd:          r.cfg.Root,
		Prompt:       SlackMCPPrompt(l),
		Model:        r.cfg.Model,
		OutputSchema: slackMCPSchema,
		// The Slack server's read tools and nothing else: no other MCP
		// tool is in the session, and permissions.mcp is not consulted,
		// because this allow-list is the whole of what the reading needs.
		Policy:         &provider.PermissionPolicy{Root: r.cfg.Root, MCPAllow: slackMCPReadTools, Mode: provider.ModeTriage},
		Mode:           provider.ModeTriage,
		Budget:         provider.Budget{MaxTurns: slackMCPMaxTurns, MaxMinutes: 2},
		MCPStrict:      true,
		UserMCPServers: []provider.UserMCPServer{r.server},
		Binary:         binary,
	})
	if err != nil {
		return out, fmt.Errorf("start the Slack MCP reading: %w", err)
	}
	_ = sess.CloseInput()
	defer time.AfterFunc(2*time.Minute+composeIntentGrace, sess.Cancel).Stop()

	var final json.RawMessage
	for ev := range sess.Events() {
		if ev.Kind == provider.EvFinal && ev.Final != nil {
			final = ev.Final
		}
	}
	res, waitErr := sess.Wait()
	if final == nil {
		final = res.Final
	}
	if final == nil && strings.TrimSpace(res.Text) != "" {
		final = json.RawMessage(objectIn(res.Text))
	}
	if final == nil {
		switch {
		case waitErr != nil:
			return out, fmt.Errorf("the Slack MCP reading gave no answer: %w", waitErr)
		case res.ExitErr != nil:
			return out, fmt.Errorf("the Slack MCP reading gave no answer: %v", res.ExitErr)
		}
		return out, errors.New("the Slack MCP reading gave no answer")
	}
	return threadFromMCP(l, final)
}

// threadFromMCP turns the reading into the thread the Web API path would
// have returned: the linked message first and carrying the link's ts, so
// the resolver scans it first, and the model's refs beside the texts.
func threadFromMCP(l slack.Link, data []byte) (slack.Thread, error) {
	var doc struct {
		Messages []struct {
			Author string `json:"author"`
			Time   string `json:"time"`
			Text   string `json:"text"`
		} `json:"messages"`
		Refs []string `json:"refs"`
	}
	out := slack.Thread{Link: l}
	if err := json.Unmarshal(data, &doc); err != nil {
		return out, fmt.Errorf("the Slack MCP reading is not valid JSON: %w", err)
	}
	if len(doc.Messages) == 0 {
		return out, errors.New("the Slack MCP reading found no message at that link")
	}
	if len(doc.Messages) > slack.MaxMessages {
		doc.Messages = doc.Messages[:slack.MaxMessages]
		out.Truncated = true
	}
	for i, m := range doc.Messages {
		msg := slack.Message{Author: strings.TrimSpace(m.Author), Text: m.Text, At: parseSlackTime(m.Time)}
		if i == 0 {
			msg.TS = l.TS
		}
		out.Messages = append(out.Messages, msg)
	}
	out.IsThread = len(out.Messages) > 1
	for _, ref := range doc.Refs {
		if ref = strings.TrimSpace(ref); ref != "" {
			out.Refs = append(out.Refs, ref)
		}
	}
	return out, nil
}

// parseSlackTime reads RFC 3339 or a Slack ts; anything else is no time.
func parseSlackTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 1e9 {
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
	}
	return time.Time{}
}

// slackViaMCP is the MCP reader a workspace gets when it opted `slack` into
// mcp.userServers and runs the claude provider, or nil. A Slack server the
// CLI no longer has is an error, which the resolver reports in place of
// the thread.
func slackViaMCP(cfg *config.Config, prov provider.Provider) (SlackReader, error) {
	if !cfg.HasUserServer(slackMCPServer) || prov == nil || prov.Name() != "claude" {
		return nil, nil
	}
	servers, err := provider.UserServersFor(nil, []string{slackMCPServer})
	if err != nil {
		return nil, err
	}
	return &slackMCPReader{prov: prov, cfg: cfg, server: servers[0]}, nil
}

// SlackReaderFor is the Slack reader a workspace has: the Web API with
// sources.slack.token when that is set, else the operator's Slack MCP
// server when mcp.userServers names it and the provider is claude, else
// nil.
func SlackReaderFor(cfg *config.Config, prov provider.Provider) (SlackReader, error) {
	if cfg.Sources.Slack != nil {
		return SlackFor(cfg)
	}
	return slackViaMCP(cfg, prov)
}
