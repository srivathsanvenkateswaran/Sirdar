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

// slackMCPMaxTurns is the reading session's ceiling: a ToolSearch turn
// (the CLI loads an MCP server's tools on demand), slack_read_thread, the
// slack_read_channel fallback when the thread read errors, the answer, and
// room for a call retried once. Four left no room for the fallback.
const slackMCPMaxTurns = 6

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
          "text": {"type": "string", "description": "The message text exactly as written, with attachment and file titles appended on their own lines."},
          "files": {
            "type": "array",
            "description": "The files attached to the message, when the tool result lists them.",
            "items": {
              "type": "object",
              "additionalProperties": false,
              "required": ["name"],
              "properties": {
                "name": {"type": "string"},
                "url": {"type": "string", "description": "The file's url_private or permalink when the tool result gives one; empty otherwise."}
              }
            }
          }
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

// SlackMCPPrompt is what the reading session is asked. slack_read_thread
// comes first, with the ts the permalink carries: it answers a thread's
// parent and a lone message alike, and a DM's D… id is a channel_id like
// any other. slack_read_channel is the fallback only, with a window one
// microsecond either side of the ts, because its oldest and latest bounds
// are exclusive and a window of the ts itself returns nothing.
func SlackMCPPrompt(l slack.Link) string {
	ts := tsOf(l.TS)
	thread := ts
	if l.ThreadTS != "" {
		thread = l.ThreadTS
	}
	oldest, latest := tsWindow(ts)
	var b strings.Builder
	b.WriteString("Read one Slack message, and the thread it belongs to, with the Slack MCP tools, and answer only with the JSON object the schema describes.\n\n")
	fmt.Fprintf(&b, "- Link: %s\n- Channel: %s\n- Message ts: %s\n", l.URL, l.Channel, ts)
	if l.ThreadTS != "" {
		fmt.Fprintf(&b, "- Thread ts: %s\n", l.ThreadTS)
	}
	b.WriteString("\nDo this, in this order:\n\n")
	fmt.Fprintf(&b, "1. Call mcp__slack__slack_read_thread with channel_id %q and message_ts %q. ", l.Channel, thread)
	b.WriteString("The channel id is valid as it is, a direct-message id starting with D included; do not look the channel up first. ")
	b.WriteString("It returns the message and every reply; a message with no replies comes back alone, which is a complete answer.\n")
	fmt.Fprintf(&b, "2. Only if that call returns an error, call mcp__slack__slack_read_channel with channel_id %q, oldest %q, latest %q and limit 1, and keep that one message.\n", l.Channel, oldest, latest)
	b.WriteString("3. Answer. Call no other tool, and nothing that writes.\n\n")
	b.WriteString("- messages: every message read, the linked one first and then the rest oldest first, each with its author, time and text exactly as written, and its files (name, and url when the result gives one). Do not translate or summarise.\n")
	b.WriteString("- refs: every tracker key (letters, a dash, digits, like SBX-1) and helpdesk number (#28310) the messages name, linked message first; [] when there are none.\n")
	b.WriteString("- If both calls fail, answer with messages [] and refs [], and say in one sentence before the JSON what the tools returned.\n")
	return b.String()
}

// tsWindow is the exclusive oldest/latest pair one microsecond either side
// of a dotted Slack ts: 1712345678.901234 gives 1712345678.901233 and
// 1712345678.901235. A ts it cannot read is returned as both bounds.
func tsWindow(ts string) (string, string) {
	sec, frac, ok := strings.Cut(ts, ".")
	if !ok || len(frac) != 6 {
		return ts, ts
	}
	us, err := strconv.ParseInt(sec+frac, 10, 64)
	if err != nil || us < 1e6 {
		return ts, ts
	}
	dotted := func(v int64) string {
		s := strconv.FormatInt(v, 10)
		return s[:len(s)-6] + "." + s[len(s)-6:]
	}
	return dotted(us - 1), dotted(us + 1)
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
	log := openIntakeLog(time.Now())
	defer log.close()
	promptText := SlackMCPPrompt(l)
	log.write(map[string]any{
		"record": "request", "link": l.URL, "channel": l.Channel, "ts": tsOf(l.TS), "threadTs": l.ThreadTS,
		"maxTurns": slackMCPMaxTurns, "allow": slackMCPReadTools, "model": r.cfg.Model, "prompt": promptText,
	})
	sess, err := r.prov.Start(ctx, provider.SessionSpec{
		Cwd:          r.cfg.Root,
		Prompt:       promptText,
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
		log.write(map[string]any{"record": "outcome", "error": err.Error()})
		return out, fmt.Errorf("start the Slack MCP reading: %w", err)
	}
	// Input stays open until the answer arrives. The session's permission
	// prompts travel over it — every Slack tool call is a can_use_tool
	// request the policy answers on stdin — and closing it at the start,
	// as this used to, failed each call with "Tool permission request
	// failed: AbortError: Stream closed". The model, left with nothing,
	// answered an empty list, which read as "no message at that link".
	defer time.AfterFunc(2*time.Minute+composeIntentGrace, sess.Cancel).Stop()

	var final json.RawMessage
	var said strings.Builder // the model's own words, for a reading that found nothing
	var lastTool string      // the last tool result, for the same
	for ev := range sess.Events() {
		log.event(ev)
		switch {
		case ev.Kind == provider.EvFinal:
			_ = sess.CloseInput()
			if ev.Final != nil {
				final = ev.Final
			}
			if ev.Text != "" && said.Len() == 0 {
				said.WriteString(ev.Text)
			}
		case ev.Kind == provider.EvAssistantText && !ev.Delta:
			if ev.Replace {
				said.Reset()
			}
			said.WriteString(ev.Text)
		case ev.Kind == provider.EvToolFinished && strings.TrimSpace(ev.Text) != "":
			lastTool = ev.Text
		}
	}
	res, waitErr := sess.Wait()
	outcome := map[string]any{"record": "outcome", "turns": res.Usage.Turns, "text": res.Text}
	if waitErr != nil {
		outcome["error"] = waitErr.Error()
	}
	if res.ExitErr != nil {
		outcome["exit"] = res.ExitErr.Error()
	}
	if len(res.StderrTail) > 0 {
		outcome["stderr"] = res.StderrTail
	}
	log.write(outcome)
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
	why := strings.TrimSpace(said.String())
	if why == "" || strings.HasPrefix(why, "{") {
		why = ""
		if t := strings.TrimSpace(lastTool); t != "" {
			why = "the last tool result: " + t
		}
	}
	return threadFromMCP(l, final, why)
}

// emptyReadingMax is how much of the model's own words a reading that found
// nothing carries into its error, which the chip shows as the reason.
const emptyReadingMax = 200

// threadFromMCP turns the reading into the thread the Web API path would
// have returned: the linked message first and carrying the link's ts, so
// the resolver scans it first, and the model's refs beside the texts. why
// is what the model said, or the last tool result, for a reading with no
// messages in it: that is an error, and the error says what happened.
func threadFromMCP(l slack.Link, data []byte, why string) (slack.Thread, error) {
	var doc struct {
		Messages []struct {
			Author string `json:"author"`
			Time   string `json:"time"`
			Text   string `json:"text"`
			Files  []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"files"`
		} `json:"messages"`
		Refs []string `json:"refs"`
	}
	out := slack.Thread{Link: l}
	if err := json.Unmarshal(data, &doc); err != nil {
		return out, fmt.Errorf("the Slack MCP reading is not valid JSON: %w", err)
	}
	if len(doc.Messages) == 0 {
		why = strings.Join(strings.Fields(why), " ")
		if why == "" {
			return out, errors.New("the Slack MCP reading returned no messages and said nothing about why")
		}
		if r := []rune(why); len(r) > emptyReadingMax {
			why = string(r[:emptyReadingMax]) + "…"
		}
		return out, fmt.Errorf("the Slack MCP reading returned no messages: %s", why)
	}
	if len(doc.Messages) > slack.MaxMessages {
		doc.Messages = doc.Messages[:slack.MaxMessages]
		out.Truncated = true
	}
	for i, m := range doc.Messages {
		msg := slack.Message{Author: mcpAuthor(m.Author), Text: m.Text, At: parseSlackTime(m.Time)}
		for _, f := range m.Files {
			if name := strings.TrimSpace(f.Name); name != "" {
				msg.Files = append(msg.Files, slack.File{Name: name, URL: strings.TrimSpace(f.URL)})
			}
		}
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

// mcpAuthor is the name in an author the Slack MCP writes as
// "Name <address> (U0123ABCD)": the name alone, the way the Web API path
// names an author, so the address and the user id stay out of the bundle,
// the note and the run list.
func mcpAuthor(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:i])
	}
	if i := strings.Index(s, " <"); i > 0 && strings.HasSuffix(s, ">") {
		s = strings.TrimSpace(s[:i])
	}
	return s
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
