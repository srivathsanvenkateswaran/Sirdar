package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// What one piece of text was recognised as, in the order the resolver
// prefers them when the text holds more than one.
const (
	InputKey            = "key"
	InputTrackerURL     = "tracker-url"
	InputHelpdeskNumber = "helpdesk-number"
	InputHelpdeskURL    = "helpdesk-url"
	InputSlack          = "slack"
	InputText           = "text"
)

// IntakeStep is one hop of a resolution: what it started from, what it
// arrived at, and how. The chip under the composer draws the hops as a
// chain — "Slack thread → #28310 → SBX-1 · matched by title".
type IntakeStep struct {
	From string `json:"from"`
	To   string `json:"to"`
	// How says why the hop holds, in the words the chip shows:
	// "matched by title", "from Zoho field cf_jira_ticket_id".
	How string `json:"how,omitempty"`
	// Source is where the hop was found: "slack", "helpdesk record",
	// "tracker id", "tracker search", "recent tickets", "remembered" or
	// "model".
	Source string `json:"source,omitempty"`
}

// SlackIntake is what a Slack link resolved through: the link and how many
// messages were read from it.
type SlackIntake struct {
	URL      string `json:"url"`
	Messages int    `json:"messages"`
	Thread   bool   `json:"thread"`
}

// Intake is what a piece of pasted text resolved to: the tracker key a
// session runs on, the helpdesk ticket on the other side of it, the hops
// that got there, and — when there is no key — the reason, in words a
// person reads.
//
// Key empty with a Reason is an ordinary answer and not an error: the
// composer prints the reason under the box while somebody is still typing.
type Intake struct {
	Input          string       `json:"input"`
	Key            string       `json:"key"`
	HelpdeskNumber string       `json:"helpdeskNumber,omitempty"`
	HelpdeskID     string       `json:"helpdeskId,omitempty"`
	Via            []IntakeStep `json:"via"`
	Summary        string       `json:"summary,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	// Subject is the helpdesk subject or tracker title read on the way,
	// when one was.
	Subject string       `json:"subject,omitempty"`
	Slack   *SlackIntake `json:"slack,omitempty"`

	// Mode, Instruction and Confidence are set only when nothing in the
	// text was recognised and the model read it instead.
	Mode        string  `json:"mode,omitempty"`
	Instruction string  `json:"instruction,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`

	// Repos are the repositories other than the workspace's own that the
	// text read on the way mentions — the tracker record, the helpdesk
	// subject, the Slack thread — matched to repos: by origin. The chip
	// adds "· mentions Acme.Web (companion repo)" for each.
	Repos []repos.Mention `json:"repos,omitempty"`

	thread *slack.Thread
	// texts is what the resolution read that a person wrote, for Repos.
	texts []string
}

// SlackMarkdown is the Slack thread this intake read, rendered for the
// bundle's slack.md; empty when it read none.
func (in Intake) SlackMarkdown() string {
	if in.thread == nil {
		return ""
	}
	return slack.Markdown(*in.thread)
}

// SlackNotConfigured is the reason a Slack link gets in a workspace with no
// sources.slack block.
const SlackNotConfigured = "Slack is not configured: set sources.slack.token (a Slack user token with channels:history, groups:history) in config.yaml, or add `slack` to mcp.userServers"

// --- recognising --------------------------------------------------------

type intakeRef struct {
	kind  string
	value string
	link  slack.Link
}

var (
	urlIn          = regexp.MustCompile(`https?://[^\s<>"]+`)
	helpdeskNumIn  = regexp.MustCompile(`#([0-9]{4,})\b`)
	uuidIn         = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	loneKey        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+-[0-9]+$`)
	zohoHost       = regexp.MustCompile(`^desk\.zoho\.[a-z]{2,3}(\.[a-z]{2})?$`)
	zohoPathID     = regexp.MustCompile(`/tickets/(?:details/)?([0-9]{4,})(?:/|$)`)
	zohoFragmentID = regexp.MustCompile(`(?:^|/)Cases/dv/([0-9]{4,})(?:/|$)`)
	titleNumber    = regexp.MustCompile(`^\s*#([0-9]+)\b`)
	githubShortRef = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+#[0-9]+\b`)
)

// recognise finds every reference the text carries and returns them in the
// resolver's order of preference: a tracker key, a tracker URL ending in a
// key, a helpdesk number, a helpdesk URL, a Slack permalink. Within a kind
// the first in the text comes first.
func recognise(text string) []intakeRef {
	var keys, trackerURLs, numbers, helpdeskURLs, slacks []intakeRef

	// URLs first, and blanked out of what the bare patterns then read: a
	// key or a number inside a URL belongs to the URL.
	rest := []byte(text)
	for _, span := range urlIn.FindAllStringIndex(text, -1) {
		raw := strings.TrimRight(text[span[0]:span[1]], ".,;:)]}'")
		for i := span[0]; i < span[1]; i++ {
			rest[i] = ' '
		}
		if l, ok := slack.FindLink(raw); ok {
			slacks = append(slacks, intakeRef{kind: InputSlack, value: l.URL, link: l})
			continue
		}
		if id := helpdeskIDInURL(raw); id != "" {
			helpdeskURLs = append(helpdeskURLs, intakeRef{kind: InputHelpdeskURL, value: id})
			continue
		}
		if key := keyInURL(raw); key != "" {
			trackerURLs = append(trackerURLs, intakeRef{kind: InputTrackerURL, value: key})
		}
	}
	blankOut := func(s string) string { return strings.Repeat(" ", len(s)) }
	plain := uuidIn.ReplaceAllStringFunc(string(rest), blankOut)
	// A GitHub reference, acme/web#1234, is a pull request or an issue,
	// not helpdesk ticket #1234.
	plain = githubShortRef.ReplaceAllStringFunc(plain, blankOut)
	trimmed := strings.TrimSpace(plain)
	if loneKey.MatchString(trimmed) {
		keys = append(keys, intakeRef{kind: InputKey, value: strings.ToUpper(trimmed)})
	}
	for _, k := range trackerKeyIn.FindAllString(plain, -1) {
		keys = append(keys, intakeRef{kind: InputKey, value: k})
	}
	for _, m := range helpdeskNumIn.FindAllStringSubmatch(plain, -1) {
		numbers = append(numbers, intakeRef{kind: InputHelpdeskNumber, value: m[1]})
	}

	var out []intakeRef
	for _, group := range [][]intakeRef{keys, trackerURLs, numbers, helpdeskURLs, slacks} {
		out = append(out, group...)
	}
	return out
}

// keyInURL is the key at the end of a URL's path — `…/browse/SBX-1` — or
// empty when the last segment is not one.
func keyInURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	last := segments[len(segments)-1]
	if loneKey.MatchString(last) {
		return strings.ToUpper(last)
	}
	return ""
}

// helpdeskIDInURL reads the record id out of a Zoho Desk link, in either of
// the two shapes Desk writes one: the agent UI's
// desk.zoho.com/agent/<portal>/<dept>/tickets/details/<id> and the older
// desk.zoho.com/support/<portal>/ShowHomePage.do#Cases/dv/<id>.
func helpdeskIDInURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !zohoHost.MatchString(strings.ToLower(u.Hostname())) {
		return ""
	}
	if m := zohoFragmentID.FindStringSubmatch(u.Fragment); m != nil {
		return m[1]
	}
	if m := zohoPathID.FindStringSubmatch(u.Path); m != nil {
		return m[1]
	}
	return ""
}

// --- resolving ----------------------------------------------------------

// SlackReader is the one call the resolver makes on Slack; *slack.Client
// is one.
type SlackReader interface {
	Read(ctx context.Context, l slack.Link) (slack.Thread, error)
}

// intakeResolver holds what one resolution reads from. Every field may be
// nil: a workspace with no helpdesk still resolves a key, and one with no
// Slack block answers a Slack link with the setting to add.
type intakeResolver struct {
	tracker      source.Tracker
	helpdesk     source.Helpdesk
	helpdeskName string
	trackerField string
	slack        SlackReader
	slackErr     error
	cache        *intakeCache
	repos        []repos.Repo
	fallback     func(ctx context.Context, text string) (ComposedIntent, error)
}

// recentLimit is how many of the tracker's newest tickets the last-resort
// match reads.
const recentLimit = 200

// searchLimit is how many tickets the tracker-search step asks for.
const searchLimit = 20

func (r *intakeResolver) resolve(ctx context.Context, text string) (Intake, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Intake{}, fmt.Errorf("%w: there is nothing to resolve", ErrInvalidArgument)
	}
	if len(text) > ComposeIntentMax {
		text = text[:ComposeIntentMax]
	}
	refs := recognise(text)
	var in Intake
	var err error
	if len(refs) == 0 {
		in, err = r.fromText(ctx, text)
	} else {
		in, err = r.fromRef(ctx, refs[0])
		if err == nil {
			in.Summary = intakeSummary(in)
		}
	}
	if err != nil {
		return in, err
	}
	in.Repos = r.mentions(append([]string{text}, in.texts...))
	if in.Summary != "" {
		for _, m := range in.Repos {
			in.Summary += " · " + m.Phrase()
		}
	}
	return in, nil
}

// mentions is every repository other than the workspace's own that the
// texts name, matched against repos: by origin.
func (r *intakeResolver) mentions(texts []string) []repos.Mention {
	var out []repos.Mention
	for _, m := range repos.Mentions(strings.Join(texts, "\n"), r.repos) {
		if m.Status != repos.StatusWorkspace {
			out = append(out, m)
		}
	}
	return out
}

func (r *intakeResolver) fromRef(ctx context.Context, ref intakeRef) (Intake, error) {
	switch ref.kind {
	case InputKey, InputTrackerURL:
		return r.fromKey(ctx, ref.kind, ref.value), nil
	case InputHelpdeskNumber:
		return r.fromHelpdesk(ctx, ref.kind, ref.value, ""), nil
	case InputHelpdeskURL:
		return r.fromHelpdesk(ctx, ref.kind, "", ref.value), nil
	case InputSlack:
		return r.fromSlack(ctx, ref.link), nil
	}
	return Intake{}, fmt.Errorf("%w: unknown reference kind %q", ErrInvalidArgument, ref.kind)
}

// fromKey is the direction that already existed: the tracker record names
// its helpdesk ticket. A tracker that cannot be read leaves the key as it
// was typed, because the key is what the session runs on either way.
func (r *intakeResolver) fromKey(ctx context.Context, kind, key string) Intake {
	in := Intake{Input: kind, Key: key}
	if r.tracker == nil {
		return in
	}
	tt, err := r.tracker.Get(ctx, key)
	if err != nil {
		return in
	}
	in.Subject = tt.Title
	in.texts = append(in.texts, tt.Title, tt.Description)
	in.HelpdeskID = tt.HelpdeskRef
	if m := titleNumber.FindStringSubmatch(tt.Title); m != nil {
		in.HelpdeskNumber = m[1]
	}
	return in
}

// fromHelpdesk is the missing direction: which tracker issue a helpdesk
// ticket belongs to. It tries, in order, the pairs it remembers, the
// helpdesk record's own fields (a key, or the tracker's own id), the
// tracker's search, and a match over the tracker's newest tickets.
func (r *intakeResolver) fromHelpdesk(ctx context.Context, kind, number, id string) Intake {
	in := Intake{Input: kind, HelpdeskNumber: number, HelpdeskID: id}
	if r.tracker == nil && r.helpdesk == nil {
		in.Reason = "this workspace reads no tracker or helpdesk, so " + helpdeskLabel(number, id) + " cannot be looked up"
		return in
	}
	if hit, ok := r.cache.get(number, id); ok {
		in.Key = hit.Key
		in.Via = []IntakeStep{{From: helpdeskLabel(number, id), To: hit.Key, How: hit.How, Source: "remembered"}}
		if in.HelpdeskNumber == "" {
			in.HelpdeskNumber = hit.Number
		}
		return in
	}

	var helpdeskNote string
	var key, how, src string
	if r.helpdesk != nil {
		lookup := id
		if lookup == "" {
			lookup = number
		}
		hd, err := r.helpdesk.Get(ctx, lookup)
		switch {
		case err == nil:
			in.Subject = hd.Subject
			in.texts = append(in.texts, hd.Subject)
			if in.HelpdeskID == "" {
				in.HelpdeskID = hd.ID
			}
			if in.HelpdeskNumber == "" {
				in.HelpdeskNumber = hd.Fields["ticketNumber"]
			}
			link := linkInRecord(hd, r.trackerField)
			switch {
			case link.key != "":
				key, how, src = link.key, r.fromField(link.field), "helpdesk record"
			case link.uuid != "":
				if getter, ok := r.tracker.(source.IDGetter); ok {
					if tt, err := getter.GetByID(ctx, link.uuid); err == nil && tt.Key != "" {
						key, how, src = tt.Key, r.fromField(link.field)+", by tracker id", "tracker id"
					}
				}
			}
		case isCode(err, source.NotFound):
			helpdeskNote = "the helpdesk has no ticket " + lookup
		case isCode(err, source.Unsupported):
		default:
			helpdeskNote = "the helpdesk could not be read (" + err.Error() + ")"
		}
	}
	if key == "" && r.tracker != nil {
		key, how, src = r.searchTracker(ctx, in.HelpdeskNumber, in.HelpdeskID)
	}

	from := helpdeskLabel(in.HelpdeskNumber, in.HelpdeskID)
	if key == "" {
		switch {
		case r.tracker == nil && helpdeskNote != "":
			in.Reason = helpdeskNote
		case r.tracker == nil:
			in.Reason = "the helpdesk record for " + from + " names no tracker issue, and this workspace reads no tracker to search"
		case in.HelpdeskNumber == "" && in.HelpdeskID != "":
			in.Reason = "no tracker issue links to helpdesk ticket " + in.HelpdeskID + " (looked at the helpdesk record and the tracker's newest " + strconv.Itoa(recentLimit) + " tickets)"
		default:
			in.Reason = "no tracker issue names " + from + " (looked at the helpdesk record and the tracker's newest " + strconv.Itoa(recentLimit) + " tickets)"
		}
		return in
	}
	in.Key = key
	in.Via = []IntakeStep{{From: from, To: key, How: how, Source: src}}
	r.cache.put(in.HelpdeskNumber, in.HelpdeskID, intakeCacheEntry{Key: key, How: how, Number: in.HelpdeskNumber})
	return in
}

// fromField is the chip's words for a link read off a helpdesk field.
func (r *intakeResolver) fromField(field string) string {
	if field == "" {
		return "from the " + r.helpdeskName + " subject"
	}
	return "from " + r.helpdeskName + " field " + field
}

// searchTracker asks the tracker for the issue a helpdesk ticket belongs
// to: first with the number as a search query, which an adapter may honour
// or ignore, then over its newest tickets. Either answer is checked rather
// than trusted — an adapter that ignored the query answered with its
// ordinary list — by the record's helpdesk link, then by a title that
// starts with the number, which is how a synced tracker issue is titled.
func (r *intakeResolver) searchTracker(ctx context.Context, number, id string) (key, how, src string) {
	query := id
	if number != "" {
		query = "#" + number
	}
	if query == "" {
		return "", "", ""
	}
	if list, err := r.tracker.List(ctx, source.ListFilter{Query: query, Limit: searchLimit, Order: source.OrderNewest}); err == nil {
		if key, how := matchHelpdesk(list, number, id); key != "" {
			return key, how, "tracker search"
		}
	}
	if list, err := r.tracker.List(ctx, source.ListFilter{Limit: recentLimit, Order: source.OrderNewest}); err == nil {
		if key, how := matchHelpdesk(list, number, id); key != "" {
			return key, how, "recent tickets"
		}
	}
	return "", "", ""
}

// matchHelpdesk finds the tracker issue for a helpdesk ticket in a list,
// newest first: a record whose helpdesk link is the ticket wins over one
// whose title merely starts with its number.
func matchHelpdesk(list []ticket.TrackerTicket, number, id string) (string, string) {
	list = append([]ticket.TrackerTicket(nil), list...)
	sort.SliceStable(list, func(i, j int) bool { return newestOf(list[i]).After(newestOf(list[j])) })
	for _, tt := range list {
		ref := strings.TrimSpace(tt.HelpdeskRef)
		if ref != "" && ((id != "" && ref == id) || (number != "" && ref == number)) {
			return tt.Key, "matched by helpdesk link"
		}
	}
	if number == "" {
		return "", ""
	}
	for _, tt := range list {
		if m := titleNumber.FindStringSubmatch(tt.Title); m != nil && m[1] == number {
			return tt.Key, "matched by title"
		}
	}
	return "", ""
}

func newestOf(tt ticket.TrackerTicket) time.Time {
	if !tt.CreatedAt.IsZero() {
		return tt.CreatedAt
	}
	return tt.UpdatedAt
}

// fromSlack reads the message a Slack link points at and its thread, finds
// the first ticket reference in what was written, and resolves onward.
func (r *intakeResolver) fromSlack(ctx context.Context, l slack.Link) Intake {
	in := Intake{Input: InputSlack}
	if r.slack == nil {
		in.Reason = SlackNotConfigured
		if r.slackErr != nil {
			// The token did not resolve, or the slack MCP server the
			// workspace opted in is gone from the Claude CLI; the error
			// says which.
			in.Reason = "Slack could not be read: " + r.slackErr.Error()
		}
		return in
	}
	th, err := r.slack.Read(ctx, l)
	if err != nil {
		in.Reason = "the Slack link could not be read: " + err.Error()
		return in
	}
	in.thread = &th
	in.texts = append(th.Texts(), th.Refs...)
	in.Slack = &SlackIntake{URL: l.URL, Messages: len(th.Messages), Thread: th.IsThread}
	label := "Slack message"
	if th.IsThread {
		label = "Slack thread"
	}
	reason := "the " + label + " names no ticket key, helpdesk number or helpdesk link"
	if v, ok := r.slack.(interface{ ViaMCP() bool }); ok && v.ViaMCP() {
		label = "Slack (via MCP)"
	}

	var found *intakeRef
	for _, ref := range recognise(strings.Join(append(th.Texts(), th.Refs...), "\n")) {
		if ref.kind != InputSlack {
			ref := ref
			found = &ref
			break
		}
	}
	if found == nil {
		in.Reason = reason
		return in
	}
	inner, err := r.fromRef(ctx, *found)
	if err != nil {
		in.Reason = err.Error()
		return in
	}
	inner.Input = InputSlack
	inner.Slack = in.Slack
	inner.thread = in.thread
	inner.texts = append(append([]string(nil), in.texts...), inner.texts...)
	first := IntakeStep{From: label, Source: "slack"}
	switch found.kind {
	case InputKey, InputTrackerURL:
		first.To = found.value
	default:
		first.To = helpdeskLabel(inner.HelpdeskNumber, inner.HelpdeskID)
		if found.kind == InputHelpdeskNumber {
			first.To = "#" + found.value
		}
	}
	inner.Via = append([]IntakeStep{first}, inner.Via...)
	return inner
}

// fromText is the fallback for text with no reference in it at all: the
// model reads it, when the caller allows that.
func (r *intakeResolver) fromText(ctx context.Context, text string) (Intake, error) {
	in := Intake{Input: InputText}
	if r.fallback == nil {
		in.Reason = "no ticket key, helpdesk number or link in that"
		return in, nil
	}
	read, err := r.fallback(ctx, text)
	if err != nil {
		return in, err
	}
	in.Key, in.Mode, in.Instruction, in.Confidence = read.Key, read.Mode, read.Instruction, read.Confidence
	if in.Key == "" {
		in.Reason = "no ticket key, helpdesk number or link in that"
		return in, nil
	}
	in.Via = []IntakeStep{{From: "your words", To: in.Key, How: "read by the model", Source: "model"}}
	in.Summary = intakeSummary(in)
	return in, nil
}

// intakeSummary is the chip: the chain of hops, how the last one held, and
// the helpdesk ticket beside a key that was named outright.
func intakeSummary(in Intake) string {
	if in.Key == "" {
		return ""
	}
	var b strings.Builder
	if len(in.Via) == 0 {
		b.WriteString(in.Key)
	} else {
		b.WriteString(in.Via[0].From)
		for _, s := range in.Via {
			b.WriteString(" → ")
			b.WriteString(s.To)
		}
		if how := in.Via[len(in.Via)-1].How; how != "" {
			b.WriteString(" · ")
			b.WriteString(how)
		}
	}
	lastFromKey := len(in.Via) == 0 || in.Via[len(in.Via)-1].To == in.Key && in.Via[len(in.Via)-1].Source == "slack"
	if lastFromKey && (in.HelpdeskNumber != "" || in.HelpdeskID != "") {
		b.WriteString(" · ")
		b.WriteString(helpdeskLabel(in.HelpdeskNumber, in.HelpdeskID))
	}
	return b.String()
}

// helpdeskLabel is how a helpdesk ticket is named in a chip: its number
// when known, else its record id.
func helpdeskLabel(number, id string) string {
	if number != "" {
		return "#" + number
	}
	if id != "" {
		return "helpdesk ticket " + id
	}
	return "the helpdesk ticket"
}

func isCode(err error, code source.Code) bool {
	var serr *source.Error
	return errors.As(err, &serr) && serr.Code == code
}

// --- reading a helpdesk record's link ------------------------------------

// recordLink is what a helpdesk record says about its tracker issue: a key,
// or the tracker's own id when the field holds that instead, and the field
// it was read from ("" for the subject).
type recordLink struct {
	key, uuid, field string
}

// linkInRecord reads the tracker link off a helpdesk record. The pinned
// field (sources.helpdesk.trackerField) is read first; then any field whose
// name says it is a link — janus, jira, ticket — then the rest, each tier in
// name order so the answer does not move with map iteration; then the
// subject. A field may hold several keys, and the newest-numbered wins. A
// field holding a UUID rather than a key is reported as such, for a
// tracker that can be asked by id.
func linkInRecord(hd ticket.HelpdeskTicket, pin string) recordLink {
	pin = strings.TrimPrefix(strings.TrimSpace(pin), "cf.")
	tier := func(name string) int {
		short := strings.TrimPrefix(name, "cf.")
		lower := strings.ToLower(short)
		switch {
		case pin != "" && short == pin:
			return 0
		case strings.Contains(lower, "janus"), strings.Contains(lower, "jira"), strings.Contains(lower, "ticket"):
			return 1
		}
		return 2
	}
	names := make([]string, 0, len(hd.Fields))
	for name := range hd.Fields {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		ti, tj := tier(names[i]), tier(names[j])
		if ti != tj {
			return ti < tj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		value := hd.Fields[name]
		field := strings.TrimPrefix(name, "cf.")
		uuids := uuidIn.FindAllString(value, -1)
		rest := uuidIn.ReplaceAllString(value, " ")
		if key := newestKey(trackerKeyIn.FindAllString(rest, -1)); key != "" {
			return recordLink{key: key, field: field}
		}
		if len(uuids) > 0 {
			return recordLink{uuid: uuids[0], field: field}
		}
	}
	if key := newestKey(trackerKeyIn.FindAllString(uuidIn.ReplaceAllString(hd.Subject, " "), -1)); key != "" {
		return recordLink{key: key}
	}
	return recordLink{}
}

// trackerKeyOf is the tracker key a helpdesk record carries, read the way
// linkInRecord reads it with no field pinned.
func trackerKeyOf(hd ticket.HelpdeskTicket) string { return linkInRecord(hd, "").key }

// newestKey is the key with the highest number, which in a field that
// collected several over a ticket's life is the one opened last.
func newestKey(keys []string) string {
	best, bestN := "", -1
	for _, k := range keys {
		i := strings.LastIndexByte(k, '-')
		n, err := strconv.Atoi(k[i+1:])
		if err != nil {
			continue
		}
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

// helpdeskDisplayName is the helpdesk's name in a chip: "Zoho" for Zoho
// Desk, the product name for another built-in, "helpdesk" for an exec
// adapter whose product Sirdar cannot know.
func helpdeskDisplayName(sc *config.SourceConfig) string {
	if sc == nil {
		return "helpdesk"
	}
	if sc.Adapter == "zohodesk" {
		return "Zoho"
	}
	if name, ok := productNames[sc.Adapter]; ok {
		return name
	}
	return "helpdesk"
}
