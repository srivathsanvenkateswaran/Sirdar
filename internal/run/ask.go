package run

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// askingPrefix marks a blocked run's Reason as a permission question: the
// call the agent wants to make, in one line. Like askedPrefix it stays out
// of a notification, since the command can quote the ticket.
const askingPrefix = "asking: "

// askReasonMax caps the call a Reason carries; the whole of it is on the
// run's Ask and in the event log.
const askReasonMax = 120

// askReason is a permission-blocked run's Reason: what the agent wants to
// run, in the words a board card can show after "blocked ·".
func askReason(ask *provider.PermissionAsk) string {
	var what string
	switch ask.Kind {
	case provider.AskBash, provider.AskFetch:
		what = ask.Summary
	case provider.AskMCP:
		what = ask.Tool
	default:
		what = strings.TrimSpace(ask.Tool + " " + ask.Summary)
	}
	if r := []rune(what); len(r) > askReasonMax {
		what = string(r[:askReasonMax]) + "…"
	}
	return askingPrefix + what
}

// Decision is the operator's answer to a permission question: allow (this
// call, once), allow_run (this call and any later one matching the same
// tool and command pattern, for the rest of the run) or deny, with an
// optional reason the agent is shown.
type Decision struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// answeredAsk is an answer as execute records it ahead of the session that
// carries it.
type answeredAsk struct {
	verdict string
	tool    string
	text    string
}

// answerAsk applies the operator's answer to the question the run blocked
// on: the grant goes where it belongs — this execute's policy for an
// allow-once, the run's state for the other two — and the returned text is
// what the resumed session opens with. answer is anything the operator
// typed beside the decision, passed on to the agent as their words.
func answerAsk(p *prepared, d Decision, answer string) (string, error) {
	ask := p.state.Ask
	if ask == nil {
		return "", fmt.Errorf("run: %s is not waiting on a permission question", p.state.RunID)
	}
	if !provider.ValidVerdict(d.Verdict) {
		return "", fmt.Errorf("run: decision %q: want allow, allow_run or deny", d.Verdict)
	}
	pa := provider.PermissionAsk(*ask)
	g := provider.GrantFor(pa, d.Verdict, d.Reason)
	if d.Verdict == provider.VerdictAllow {
		p.once = append(p.once, g)
	} else {
		p.state.Grants = append(p.state.Grants, store.Grant(g))
	}
	p.answered = &answeredAsk{verdict: d.Verdict, tool: ask.Tool, text: grantNote(g)}
	p.state.Ask = nil
	return decisionPrompt(pa, g, answer), nil
}

// grantNote is the event line an answer becomes in the transcript, in the
// operator's own voice.
func grantNote(g provider.Grant) string {
	switch g.Verdict {
	case provider.VerdictAllow:
		return "you allowed " + g.Label() + " once"
	case provider.VerdictAllowRun:
		return "you allowed " + g.Label() + " for this run"
	default:
		note := "you denied " + g.Label() + " for this run"
		if g.Reason != "" {
			note += ": " + g.Reason
		}
		return note
	}
}

// decisionPrompt is what the resumed session is told. An allow asks for the
// call again, since the session that asked was stopped before it ran; a deny
// says not to try it again, and why when the operator said.
func decisionPrompt(ask provider.PermissionAsk, g provider.Grant, answer string) string {
	call := "`" + ask.Summary + "`"
	if ask.Kind == provider.AskMCP || ask.Summary == "" {
		call = "`" + ask.Tool + "`"
	}
	var b strings.Builder
	switch g.Verdict {
	case provider.VerdictAllow:
		fmt.Fprintf(&b, "The operator allowed %s, this once. Make that call again now, then carry on and produce the JSON note.", call)
	case provider.VerdictAllowRun:
		fmt.Fprintf(&b, "The operator allowed %s, and any later call matching %s, for the rest of this run. Make that call again now, then carry on and produce the JSON note.",
			call, "`"+strings.Join(g.Patterns, "`, `")+"`")
	default:
		fmt.Fprintf(&b, "The operator denied %s", call)
		if g.Reason != "" {
			fmt.Fprintf(&b, ": %s", g.Reason)
		}
		b.WriteString(". Do not try it, or anything like it, again. Carry on without it and produce the JSON note.")
	}
	if a := strings.TrimSpace(answer); a != "" {
		b.WriteString("\n\nThe operator adds: " + a)
	}
	return b.String()
}

// freeAnswerPrompt is what the resumed session is told when the operator
// answered a permission question in words rather than with a decision: the
// call was not made, and here is what they said instead. No grant is
// recorded, so the session may ask again.
func freeAnswerPrompt(ask *store.PermissionAsk, answer string) string {
	call := ask.Summary
	if ask.Kind == provider.AskMCP || call == "" {
		call = ask.Tool
	}
	return "Your call `" + call + "` was not run. The operator answered instead: " + answer
}

// readDecision puts a permission question to the operator on the terminal
// and reads the answer: a, r or d for the three decisions, or anything else
// as a free answer.
func (r *Runner) readDecision(state store.State) (*Decision, string, error) {
	ask := state.Ask
	what := ask.Summary
	if ask.Kind == provider.AskMCP || what == "" {
		what = ask.Tool
	}
	fmt.Fprintf(r.stderr(), "[%s] the agent asks to run: %s\n[%s] %s\n[%s] allow once (a), allow for this run (r), deny (d), or type an answer: ",
		state.Key, what, state.Key, ask.Reason, state.Key)
	if r.Stdin == nil {
		return nil, "", fmt.Errorf("run: %s is waiting on a permission decision but no input is available", state.RunID)
	}
	sc := bufio.NewScanner(r.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, "", fmt.Errorf("run: read answer: %w", err)
		}
		return nil, "", fmt.Errorf("run: no answer given for %s", state.RunID)
	}
	line := strings.TrimSpace(sc.Text())
	switch strings.ToLower(line) {
	case "a", "allow":
		return &Decision{Verdict: provider.VerdictAllow}, "", nil
	case "r", "allow_run", "allow-run":
		return &Decision{Verdict: provider.VerdictAllowRun}, "", nil
	case "d", "deny":
		return &Decision{Verdict: provider.VerdictDeny}, "", nil
	case "":
		return nil, "", fmt.Errorf("run: no answer given for %s", state.RunID)
	}
	return nil, line, nil
}

// sessionGrants turns the run's kept grants, plus this execute's allow-once
// ones, into what the session's policy consults.
func sessionGrants(p *prepared) *provider.Grants {
	gs := make([]provider.Grant, 0, len(p.state.Grants)+len(p.once))
	for _, g := range p.state.Grants {
		gs = append(gs, provider.Grant(g))
	}
	gs = append(gs, p.once...)
	return provider.NewGrants(gs...)
}
