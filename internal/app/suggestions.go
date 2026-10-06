package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Suggestion is one pending playbook addition as Settings draws it: where
// it came from, the playbook it would go into, and the text it would add.
type Suggestion struct {
	ID       string  `json:"id"`
	At       string  `json:"at"`
	Source   string  `json:"source"` // remember | rca
	RunID    string  `json:"runId"`
	Key      string  `json:"key"`
	Playbook string  `json:"playbook"`
	Addition string  `json:"addition"`
	Reason   string  `json:"reason"`
	CostUSD  float64 `json:"costUSD"`
}

// rememberJudge drafts a remembered message into a playbook addition. It is
// a variable so a test answers in place of a provider.
var rememberJudge = eval.JudgeOnce

// rememberTimeout bounds the drafting call. The operator clicked a button
// and is waiting on a toast, and the fallback is always there.
const rememberTimeout = 90 * time.Second

// rememberAdditionMax is the most a drafted addition may say. A playbook
// is read by every run, so a rule that needs more than this is a playbook
// of its own for the operator to write.
const rememberAdditionMax = 600

// rememberContextMax is how much of the assistant's reply the drafting
// prompt carries: enough to say what the operator was correcting.
const rememberContextMax = 4000

// rememberMessageMax is how much of the operator's message the fallback
// keeps, so a pasted log does not become a playbook line.
const rememberMessageMax = 500

// correctionsPlaybook is where a message goes when it could not be drafted
// into a rule: kept as written, for the operator to tidy at accept time.
const correctionsPlaybook = "90-corrections.md"

// rememberSchema holds the judge to the three fields remember reads and
// nothing else, so an answer that rambles fails to parse and falls back.
var rememberSchema = []byte(`{"type":"object","additionalProperties":false,"required":["playbook","addition","reason"],"properties":{"playbook":{"type":"string"},"addition":{"type":"string"},"reason":{"type":"string"}}}`)

// suggestionOf is the line as the queue's readers see it.
func suggestionOf(l store.SuggestionLine) Suggestion {
	return Suggestion{
		ID:       l.ID,
		At:       l.At.UTC().Format(time.RFC3339),
		Source:   l.Source,
		RunID:    l.RunID,
		Key:      l.Key,
		Playbook: l.Playbook,
		Addition: l.Addition,
		Reason:   l.Reason,
		CostUSD:  l.CostUSD,
	}
}

// suggestions is the workspace's pending playbook additions, oldest first.
func (s *Service) suggestions(wsID string) ([]Suggestion, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	lines, err := store.ReadSuggestions(root)
	if err != nil {
		return nil, err
	}
	out := []Suggestion{}
	for _, l := range store.PendingSuggestions(lines) {
		out = append(out, suggestionOf(l))
	}
	return out, nil
}

// pendingSuggestion finds id among the pending proposals.
func pendingSuggestion(root, id string) (store.SuggestionLine, error) {
	lines, err := store.ReadSuggestions(root)
	if err != nil {
		return store.SuggestionLine{}, err
	}
	for _, l := range store.PendingSuggestions(lines) {
		if l.ID == id {
			return l, nil
		}
	}
	return store.SuggestionLine{}, fmt.Errorf("%w: %q", ErrNoSuchSuggestion, id)
}

// remember turns one operator message on a run into a pending playbook
// addition. index 0 is the run's instruction; any other index is the
// 1-based position of a steer or answer event, counted the way Events
// counts. One judge call drafts it as a standing rule; when that call
// fails or answers with something unusable, the message is queued as
// written under 90-corrections.md, so the click is never lost.
func (s *Service) remember(ctx context.Context, wsID, runID string, index int) (Suggestion, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return Suggestion{}, err
	}
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return Suggestion{}, err
	}
	rn, state, err := store.Open(ws.Root, runID)
	if err != nil {
		return Suggestion{}, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	// A measurement run's conversation is a replay or a run nobody
	// steered, so nothing in it is a correction the operator made.
	if state.Measurement() {
		return Suggestion{}, fmt.Errorf("%w: run %s is a measurement; its messages are not remembered", ErrBadInput, runID)
	}
	message, assistant, err := rememberedMessage(rn.Dir, state, index)
	if err != nil {
		return Suggestion{}, err
	}
	playbooks, err := s.Playbooks(wsID)
	if err != nil {
		return Suggestion{}, err
	}

	line := store.SuggestionLine{
		At:     s.now(),
		Ev:     "proposed",
		Source: "remember",
		RunID:  runID,
		Key:    state.Key,
	}
	var drafted rememberDraft
	var cost float64
	ok := false
	// A service built with no deps builder has no provider to draft with,
	// which is the fallback rather than a refusal.
	if s.build != nil {
		deps, cleanup, err := s.build(cfg, "", "", s.stderr())
		if err == nil {
			drafted, cost, ok = draftRule(ctx, deps, cfg.Model, message, assistant, playbooks)
		}
		if cleanup != nil {
			cleanup()
		}
	}
	line.CostUSD = cost
	if ok {
		line.Playbook, line.Addition, line.Reason = drafted.Playbook, drafted.Addition, drafted.Reason
	} else {
		line.Playbook = correctionsPlaybook
		line.Addition = "- " + s.now().Format("2006-01-02") + " " + state.Key + ": " +
			cutRunes(strings.Join(strings.Fields(message), " "), rememberMessageMax)
		line.Reason = "the suggestion could not be drafted; kept the message as written"
	}
	line.ID = store.SuggestionID(line.Playbook, line.Addition)

	appended, err := store.AppendSuggestion(ws.Root, line)
	if err != nil {
		return Suggestion{}, err
	}
	if !appended {
		// Already pending: the operator remembered the same thing twice,
		// and what they see is the suggestion that is queued.
		if l, err := pendingSuggestion(ws.Root, line.ID); err == nil {
			return suggestionOf(l), nil
		}
	}
	return suggestionOf(line), nil
}

// rememberDraft is the judge's answer.
type rememberDraft struct {
	Playbook string `json:"playbook"`
	Addition string `json:"addition"`
	Reason   string `json:"reason"`
}

// draftRule makes the one judge call and checks its answer. ok is false on
// any failure, and cost is what was spent either way.
func draftRule(ctx context.Context, deps runner.Deps, model, message, assistant string, playbooks []PlaybookSummary) (rememberDraft, float64, bool) {
	ctx, cancel := context.WithTimeout(ctx, rememberTimeout)
	defer cancel()
	raw, cost, err := rememberJudge(ctx, deps, model, rememberPrompt(message, assistant, playbooks), rememberSchema)
	if err != nil {
		return rememberDraft{}, cost, false
	}
	var out rememberDraft
	if json.Unmarshal(raw, &out) != nil {
		return rememberDraft{}, cost, false
	}
	out.Playbook = strings.TrimSpace(out.Playbook)
	listed := false
	for _, p := range playbooks {
		if p.Name == out.Playbook {
			listed = true
			break
		}
	}
	if !listed && !ValidPlaybookName(out.Playbook) {
		return rememberDraft{}, cost, false
	}
	out.Addition = strings.TrimSpace(cutRunes(strings.TrimSpace(out.Addition), rememberAdditionMax))
	if out.Addition == "" {
		return rememberDraft{}, cost, false
	}
	out.Reason = strings.TrimSpace(out.Reason)
	return out, cost, true
}

// rememberPrompt asks for one playbook addition drafted from the operator's
// correction.
func rememberPrompt(message, assistant string, playbooks []PlaybookSummary) string {
	var b strings.Builder
	b.WriteString("An operator corrected a support agent while it worked a ticket. Turn the correction into one addition to the agent's playbooks, so the next run gets it right without being told.\n\n")
	b.WriteString("## The operator's message\n\n")
	b.WriteString(message + "\n\n")
	b.WriteString("## What the agent had said just before\n\n")
	if strings.TrimSpace(assistant) == "" {
		b.WriteString("(nothing)\n\n")
	} else {
		b.WriteString(assistant + "\n\n")
	}
	b.WriteString("## The playbooks\n\n")
	if len(playbooks) == 0 {
		b.WriteString("(none yet)\n")
	}
	for _, p := range playbooks {
		line := "- " + p.Name + ": " + p.Title
		if p.Lede != "" {
			line += " — " + p.Lede
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n## Answer\n\n")
	b.WriteString("- playbook: the name of the listed playbook the rule belongs in, or a new one named like 20-orders.md (two digits, a hyphen, a lowercase slug, .md).\n")
	fmt.Fprintf(&b, "- addition: at most %d characters of markdown, written as a standing rule for every ticket of this kind, not as the story of this ticket.\n", rememberAdditionMax)
	b.WriteString("- reason: one sentence saying why the rule belongs there.\n")
	return b.String()
}

// rememberedMessage reads the operator's message at index and the
// assistant reply that came before it.
func rememberedMessage(dir string, state store.State, index int) (message, assistant string, err error) {
	if index < 0 {
		return "", "", fmt.Errorf("%w: index %d", ErrBadInput, index)
	}
	if index == 0 {
		if strings.TrimSpace(state.Instruction) == "" {
			return "", "", fmt.Errorf("%w: run %s was started with no instruction", ErrBadInput, state.RunID)
		}
		return state.Instruction, answerFallback(dir), nil
	}
	events, err := readRunEvents(dir)
	if err != nil {
		return "", "", err
	}
	if index > len(events) {
		return "", "", fmt.Errorf("%w: run %s has no event %d", ErrBadInput, state.RunID, index)
	}
	ev := events[index-1]
	if ev.Kind != "steer" && ev.Kind != "answer" {
		return "", "", fmt.Errorf("%w: event %d is %s, not a message from the operator", ErrBadInput, index, ev.Kind)
	}
	if strings.TrimSpace(ev.Payload.Text) == "" {
		return "", "", fmt.Errorf("%w: event %d says nothing", ErrBadInput, index)
	}
	assistant = lastAssistantText(events[:index-1])
	if assistant == "" {
		assistant = answerFallback(dir)
	}
	return ev.Payload.Text, cutRunes(assistant, rememberContextMax), nil
}

// readRunEvents is every parseable line of a run's events.jsonl, in the
// order and with the numbering Service.Events gives them.
func readRunEvents(dir string) ([]RunEvent, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("app: read events: %w", err)
	}
	var out []RunEvent
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev RunEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// lastAssistantText is the last assistant message among events: a replace
// line is a finished message on its own, a run of delta lines is one
// message in pieces, and any other line between deltas ends the message
// they were building.
func lastAssistantText(events []RunEvent) string {
	last, building := "", false
	for _, ev := range events {
		if ev.Kind != "assistant_text" {
			building = false
			continue
		}
		switch {
		case ev.Payload.Delta && building:
			last += ev.Payload.Text
		case ev.Payload.Delta:
			last, building = ev.Payload.Text, true
		default:
			last, building = ev.Payload.Text, false
		}
	}
	return strings.TrimSpace(last)
}

// answerFallback is the run's reply on disk, for a message with no
// assistant text before it in the event log.
func answerFallback(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "answer.md"))
	if err != nil {
		return ""
	}
	return cutRunes(strings.TrimSpace(string(raw)), rememberContextMax)
}

// cutRunes keeps the first n runes of s.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// acceptSuggestion files a pending suggestion into its playbook, or into
// the one the operator retargeted it to, with the text the operator may
// have edited. An existing playbook gets the addition after whatever is on
// disk now, so an edit made since the proposal is kept; a new one is
// created with a heading taken from its name.
func (s *Service) acceptSuggestion(wsID, id, playbook, addition string) (Suggestion, error) {
	root, err := s.root(wsID)
	if err != nil {
		return Suggestion{}, err
	}
	prop, err := pendingSuggestion(root, id)
	if err != nil {
		return Suggestion{}, err
	}
	playbook = strings.TrimSpace(playbook)
	if playbook == "" {
		playbook = prop.Playbook
	}
	addition = strings.TrimSpace(addition)
	if addition == "" {
		addition = strings.TrimSpace(prop.Addition)
	}
	existing, err := s.Playbooks(wsID)
	if err != nil {
		return Suggestion{}, err
	}
	listed := false
	for _, p := range existing {
		if p.Name == playbook {
			listed = true
			break
		}
	}
	// A listed playbook keeps whatever name it already has, the way
	// SavePlaybook allows; any other target has to be a name a new
	// playbook may be created under.
	if !listed && !ValidPlaybookName(playbook) {
		return Suggestion{}, fmt.Errorf(
			"%w: %q: a playbook is named like 20-logs.md — two digits, a hyphen, a lowercase slug, .md",
			ErrBadInput, playbook)
	}
	if listed {
		body, err := s.Playbook(wsID, playbook)
		if err != nil {
			return Suggestion{}, err
		}
		if _, err := s.SavePlaybook(wsID, playbook, strings.TrimRight(body, "\n")+"\n\n"+addition+"\n"); err != nil {
			return Suggestion{}, err
		}
	} else {
		if _, err := s.AddPlaybook(wsID, playbook, "# "+headingFor(playbook)+"\n\n"+addition+"\n"); err != nil {
			return Suggestion{}, err
		}
	}
	edited := playbook != prop.Playbook || addition != strings.TrimSpace(prop.Addition)
	if _, err := store.AppendSuggestion(root, store.SuggestionLine{
		At: s.now(), Ev: "accepted", ID: id, Playbook: playbook, Edited: edited,
	}); err != nil {
		return Suggestion{}, err
	}
	prop.Playbook, prop.Addition = playbook, addition
	return suggestionOf(prop), nil
}

// dismissSuggestion takes a pending suggestion out of the queue without
// touching any playbook.
func (s *Service) dismissSuggestion(wsID, id string) error {
	root, err := s.root(wsID)
	if err != nil {
		return err
	}
	if _, err := pendingSuggestion(root, id); err != nil {
		return err
	}
	_, err = store.AppendSuggestion(root, store.SuggestionLine{At: s.now(), Ev: "dismissed", ID: id})
	return err
}

// headingFor is the title a new playbook starts with, the same one the
// Settings editor gives it: "20-orders.md" becomes "Orders".
func headingFor(name string) string {
	slug := strings.TrimSuffix(name, ".md")
	if len(slug) >= 3 && slug[2] == '-' && isDigit(slug[0]) && isDigit(slug[1]) {
		slug = slug[3:]
	}
	slug = strings.TrimSpace(strings.ReplaceAll(slug, "-", " "))
	if slug == "" {
		return "Playbook"
	}
	r, size := utf8.DecodeRuneInString(slug)
	return string(unicode.ToUpper(r)) + slug[size:]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
