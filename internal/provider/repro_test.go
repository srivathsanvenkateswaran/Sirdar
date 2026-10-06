package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	pwNavigate = "mcp__playwright__browser_navigate"
	pwClick    = "mcp__playwright__browser_click"
	pwType     = "mcp__playwright__browser_type"
)

// reproPolicy is the policy a workspace like OXO.APIs gets with
// reproduction on: browser_navigate is already in permissions.mcp, which the
// guard has to override for an off-host URL.
func reproPolicy(evaluate bool) *PermissionPolicy {
	return &PermissionPolicy{
		MCPAllow: []string{pwNavigate},
		Ask:      true,
		Repro:    NewReproGuard("playwright", []string{"staging.oxo.example", "*.stg.oxo.example"}, evaluate),
	}
}

func decideJSON(p *PermissionPolicy, tool, input string) Decision {
	return p.Decide(tool, json.RawMessage(input))
}

func confine(t *testing.T, p *PermissionPolicy) {
	t.Helper()
	if d := decideJSON(p, pwNavigate, `{"url":"https://staging.oxo.example/login"}`); !d.Allow {
		t.Fatalf("on-host navigate refused: %s", d.Message)
	}
}

func TestReproNavigateOnHostAllowsAndConfines(t *testing.T) {
	p := reproPolicy(false)
	d := decideJSON(p, pwNavigate, `{"url":"https://staging.oxo.example/login"}`)
	if !d.Allow || d.Message != "repro host staging.oxo.example" {
		t.Fatalf("got %+v, want an allow saying the repro host", d)
	}
	if !p.Repro.Confined() {
		t.Fatal("guard not confined after an on-host navigate")
	}
}

func TestReproNavigateOffHostOverridesTheAllowList(t *testing.T) {
	p := reproPolicy(false)
	confine(t, p)
	d := decideJSON(p, pwNavigate, `{"url":"https://evil.example/"}`)
	if d.Allow {
		t.Fatal("off-host navigate allowed although permissions.mcp lists the tool")
	}
	if d.Ask == nil {
		t.Error("off-host navigate is not put to the operator")
	}
	want := "Sirdar policy: browser not confined to repro hosts; evil.example is not in repro.hosts (staging.oxo.example, *.stg.oxo.example)"
	if d.Message != want {
		t.Errorf("message %q, want %q", d.Message, want)
	}
	if p.Repro.Confined() {
		t.Error("guard still confined after an off-host navigate")
	}

	confine(t, p)
	d = decideJSON(p, pwNavigate, `{"url":"not a url"}`)
	if d.Allow || d.Message != "Sirdar policy: browser not confined to repro hosts; the URL does not parse" {
		t.Errorf("hostless URL: got %+v", d)
	}
	if p.Repro.Confined() {
		t.Error("guard still confined after a hostless navigate")
	}

	// The fetch matcher's rules hold here too: a repro host over plain
	// http, or behind userinfo, does not confine.
	for _, u := range []string{"http://staging.oxo.example/", "https://staging.oxo.example@evil.example/"} {
		if d := decideJSON(p, pwNavigate, `{"url":"`+u+`"}`); d.Allow {
			t.Errorf("%s allowed", u)
		}
	}
}

func TestReproWildcardHost(t *testing.T) {
	p := reproPolicy(false)
	d := decideJSON(p, pwNavigate, `{"url":"https://a.stg.oxo.example/"}`)
	if !d.Allow || d.Message != "repro host a.stg.oxo.example" {
		t.Fatalf("got %+v", d)
	}
	if d := decideJSON(p, pwNavigate, `{"url":"https://stg.oxo.example.evil.example/"}`); d.Allow {
		t.Error("a host that only begins like the wildcard was allowed")
	}
}

func TestReproInteractionOnlyWhileConfined(t *testing.T) {
	p := reproPolicy(false)
	click := `{"element":"Save","ref":"e12"}`

	d := decideJSON(p, pwClick, click)
	if d.Allow || d.Ask == nil {
		t.Fatalf("click before any navigate: got %+v, want a refusal put to the operator", d)
	}
	if !strings.Contains(d.Message, "is not in permissions.mcp") ||
		!strings.HasSuffix(d.Message, " (browser not confined to repro hosts)") {
		t.Errorf("message %q", d.Message)
	}

	confine(t, p)
	if d := decideJSON(p, pwClick, click); !d.Allow || d.Message != "repro host staging.oxo.example" {
		t.Fatalf("click while confined: got %+v", d)
	}
	for _, tool := range []string{"browser_type", "browser_press_key", "browser_select_option",
		"browser_fill_form", "browser_hover", "browser_drag", "browser_handle_dialog", "browser_wait_for"} {
		if d := decideJSON(p, "mcp__playwright__"+tool, `{"element":"Comment"}`); !d.Allow {
			t.Errorf("%s while confined: %s", tool, d.Message)
		}
	}

	d = decideJSON(p, "mcp__playwright__browser_navigate_back", `{}`)
	if d.Allow {
		t.Error("navigate_back allowed although permissions.mcp does not list it")
	}
	if p.Repro.Confined() {
		t.Fatal("still confined after navigate_back")
	}
	if d := decideJSON(p, pwClick, click); d.Allow || d.Ask == nil {
		t.Fatalf("click after navigate_back: got %+v", d)
	}

	confine(t, p)
	decideJSON(p, "mcp__playwright__browser_tabs", `{"action":"list"}`)
	if !p.Repro.Confined() {
		t.Fatal("listing tabs cleared confinement")
	}
	decideJSON(p, "mcp__playwright__browser_tabs", `{"action":"select","index":1}`)
	if p.Repro.Confined() {
		t.Fatal("selecting a tab left the guard confined")
	}
}

func TestReproEvaluateNeedsTheFlag(t *testing.T) {
	const evaluate = "mcp__playwright__browser_evaluate"
	input := `{"function":"() => document.title"}`

	p := reproPolicy(false)
	confine(t, p)
	d := decideJSON(p, evaluate, input)
	if d.Allow || d.Ask == nil {
		t.Fatalf("evaluate without the flag: got %+v, want the ordinary refusal, askable", d)
	}
	if strings.Contains(d.Message, "repro") {
		t.Errorf("evaluate without the flag was judged by the guard: %q", d.Message)
	}

	p = reproPolicy(true)
	confine(t, p)
	if d := decideJSON(p, evaluate, input); !d.Allow {
		t.Fatalf("evaluate with the flag while confined: %s", d.Message)
	}
}

func TestReproCredentialFieldIsNotAskable(t *testing.T) {
	refused := func(t *testing.T, p *PermissionPolicy, tool, input string) {
		t.Helper()
		d := decideJSON(p, tool, input)
		if d.Allow || d.Ask != nil || d.Message != CredentialRefusal {
			t.Errorf("%s %s: got %+v, want the credential refusal, not askable", tool, input, d)
		}
	}

	p := reproPolicy(false)
	confine(t, p)
	refused(t, p, pwType, `{"element":"Password","ref":"e3","text":"x"}`)
	refused(t, p, "mcp__playwright__browser_fill_form", `{"fields":[{"name":"كلمة المرور","type":"textbox","ref":"e4","value":"x"}]}`)
	refused(t, p, "mcp__playwright__browser_fill_form", `{"fields":[{"name":"Email","type":"textbox"},{"name":"Code","type":"password"}]}`)
	for _, element := range []string{"OTP code", "One-time code", "PIN", "API token", "Client secret", "رمز التحقق"} {
		refused(t, p, pwType, `{"element":"`+element+`","text":"x"}`)
	}
	if d := decideJSON(p, pwType, `{"element":"Search","text":"x"}`); !d.Allow {
		t.Errorf("typing into a search box while confined: %s", d.Message)
	}
	if d := decideJSON(p, pwType, `{"element":"Shipping address","text":"x"}`); !d.Allow {
		t.Errorf("pin\\b matched inside a word: %s", d.Message)
	}

	// Unconfined, the credential refusal still wins over the operator
	// question the ordinary rule would raise.
	p = reproPolicy(false)
	refused(t, p, pwType, `{"element":"Password","text":"x"}`)

	// An operator's allow-for-this-run on the tool does not reach a
	// credential field.
	p = reproPolicy(false)
	p.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: pwType,
		Summary: `{"element":"Comment","text":"x"}`, Patterns: []string{pwType}})
	if d := decideJSON(p, pwType, `{"element":"Comment","text":"y"}`); !d.Allow {
		t.Fatalf("the grant itself does not hold: %s", d.Message)
	}
	refused(t, p, pwType, `{"element":"Password","text":"x"}`)
	p.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: pwType,
		Summary: `{"element":"Password","text":"x"}`, Patterns: []string{pwType}})
	refused(t, p, pwType, `{"element":"Password","text":"x"}`)
}

func TestReproNilInputFallsThrough(t *testing.T) {
	p := reproPolicy(false)
	d := p.Decide(pwNavigate, nil)
	if !d.Allow {
		t.Fatalf("navigate with no arguments: %s, want permissions.mcp's allow", d.Message)
	}
	if p.Repro.Confined() {
		t.Error("an unjudged navigate confined the guard")
	}
	if d := p.Decide(pwClick, nil); d.Allow {
		t.Error("click with no arguments allowed although permissions.mcp does not list it")
	}
}

func TestReproOtherServerUntouched(t *testing.T) {
	guarded := reproPolicy(true)
	confine(t, guarded)
	plain := reproPolicy(true)
	plain.Repro = nil

	cases := []struct{ tool, input string }{
		{"mcp__grafana__query_x", `{"q":"up"}`},
		{"mcp__grafana__browser_click", `{"element":"Save"}`},
		{"mcp__playwright__browser_file_upload", `{"paths":["/etc/passwd"]}`},
		{"mcp__playwright__browser_install", `{}`},
		{"mcp__playwright__browser_snapshot", `{}`},
	}
	for _, c := range cases {
		got, want := decideJSON(guarded, c.tool, c.input), decideJSON(plain, c.tool, c.input)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: guarded %+v, plain %+v", c.tool, got, want)
		}
	}
	if !guarded.Repro.Confined() {
		t.Error("a call the guard does not govern changed confinement")
	}
}

// TestReproArgsAreReadByExactKey: Playwright reads arguments with
// JSON.parse — exact key, last duplicate wins — so the guard has to judge
// the same value. encoding/json's struct decoding matches keys without
// regard to case, which would let a second, differently-cased key carry
// the URL the guard checks while the browser follows the first.
func TestReproArgsAreReadByExactKey(t *testing.T) {
	p := reproPolicy(false)
	d := decideJSON(p, pwNavigate, `{"url":"https://evil.example/","URL":"https://staging.oxo.example/"}`)
	if d.Allow || p.Repro.Confined() {
		t.Fatalf("navigate judged on URL rather than url: %+v, confined=%v", d, p.Repro.Confined())
	}
	if d := decideJSON(p, pwNavigate, `{"URL":"https://evil.example/","url":"https://staging.oxo.example/"}`); !d.Allow {
		t.Errorf("navigate whose url is on-host refused: %s", d.Message)
	}
	confine(t, p)
	if d := decideJSON(p, pwNavigate, `{"url":"https://staging.oxo.example/","url":"https://evil.example/"}`); d.Allow || p.Repro.Confined() {
		t.Errorf("duplicate url: the last one is the one Playwright follows; got %+v", d)
	}

	confine(t, p)
	decideJSON(p, "mcp__playwright__browser_tabs", `{"action":"new","Action":"list"}`)
	if p.Repro.Confined() {
		t.Error("tabs action new read as list through a differently-cased key")
	}
	confine(t, p)
	decideJSON(p, "mcp__playwright__browser_tabs", `{"action":"list","action":"select"}`)
	if p.Repro.Confined() {
		t.Error("duplicate tabs action: the last one is the one Playwright acts on")
	}

	confine(t, p)
	for _, input := range []string{
		`{"element":"Password","Element":"Search","text":"x"}`,
		`{"element":"Search","element":"Password","text":"x"}`,
	} {
		if d := decideJSON(p, pwType, input); d.Allow || d.Message != CredentialRefusal || d.Ask != nil {
			t.Errorf("browser_type %s: got %+v", input, d)
		}
	}
	for _, input := range []string{
		`{"fields":[{"name":"Password","Name":"Search","type":"textbox"}]}`,
		`{"fields":[{"name":"Password","type":"textbox"}],"Fields":[]}`,
		`{"fields":[{"type":"password","Type":"textbox","name":"x"}]}`,
		`{"fields":["junk",{"name":"PIN","type":"textbox"}]}`,
	} {
		if d := decideJSON(p, "mcp__playwright__browser_fill_form", input); d.Allow || d.Message != CredentialRefusal || d.Ask != nil {
			t.Errorf("browser_fill_form %s: got %+v", input, d)
		}
	}
}

// TestReproArgumentlessCallUnconfines: an ACP agent may leave rawInput out
// of a permission request. The guard cannot judge such a call, so the
// ordinary rule answers, and since it may have moved the browser,
// confinement drops and nothing is auto-allowed on the strength of it.
func TestReproArgumentlessCallUnconfines(t *testing.T) {
	p := reproPolicy(false)
	confine(t, p)
	if d := p.Decide(pwNavigate, nil); !d.Allow {
		t.Fatalf("argumentless navigate: %s, want permissions.mcp's allow", d.Message)
	}
	if p.Repro.Confined() {
		t.Fatal("argumentless navigate left the guard confined")
	}

	for _, input := range []string{"", "null", "[]", `"x"`, "{"} {
		confine(t, p)
		d := p.Decide(pwType, json.RawMessage(input))
		if d.Allow {
			t.Errorf("browser_type %q while confined was auto-allowed", input)
		}
		if p.Repro.Confined() {
			t.Errorf("browser_type %q left the guard confined", input)
		}
	}
}

// TestReproScriptUnconfines: browser_run_code always, and browser_evaluate
// whenever the guard did not vouch for it, run script that can navigate,
// so after either the guard trusts nothing it saw before — whoever
// allowed the call.
func TestReproScriptUnconfines(t *testing.T) {
	const runCode, evaluate = "mcp__playwright__browser_run_code", "mcp__playwright__browser_evaluate"
	for _, c := range []struct {
		tool     string
		evaluate bool
	}{{runCode, false}, {runCode, true}, {evaluate, false}} {
		p := reproPolicy(c.evaluate)
		confine(t, p)
		if d := decideJSON(p, c.tool, `{"code":"1","function":"() => 1"}`); d.Allow || d.Ask == nil {
			t.Errorf("%s (evaluate=%v): got %+v, want the ordinary refusal, askable", c.tool, c.evaluate, d)
		}
		if p.Repro.Confined() {
			t.Errorf("%s (evaluate=%v) left the guard confined", c.tool, c.evaluate)
		}

		// The same when an operator's grant lets it through.
		p = reproPolicy(c.evaluate)
		p.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: c.tool, Patterns: []string{c.tool}})
		confine(t, p)
		if d := decideJSON(p, c.tool, `{"code":"2","function":"() => 2"}`); !d.Allow {
			t.Errorf("%s granted: %s", c.tool, d.Message)
		}
		if p.Repro.Confined() {
			t.Errorf("%s allowed by a grant left the guard confined", c.tool)
		}
	}

	// Vouched for — Evaluate on and confined — evaluate keeps confinement.
	p := reproPolicy(true)
	confine(t, p)
	if d := decideJSON(p, evaluate, `{"function":"() => 1"}`); !d.Allow || !p.Repro.Confined() {
		t.Errorf("evaluate with the flag while confined: %+v, confined=%v", d, p.Repro.Confined())
	}
}

// TestReproNavigateGrantIsPerHost: "allow for this run" on an off-host
// navigate lets the agent back to that host, not to every host, and a
// denial of one host is not a denial of another (spec §4.2).
func TestReproNavigateGrantIsPerHost(t *testing.T) {
	p := reproPolicy(false)
	d := decideJSON(p, pwNavigate, `{"url":"https://a.example/x"}`)
	if d.Ask == nil {
		t.Fatalf("off-host navigate not askable: %+v", d)
	}
	if want := []string{pwNavigate + " host:a.example"}; !reflect.DeepEqual(d.Ask.Patterns, want) {
		t.Fatalf("ask patterns %v, want %v", d.Ask.Patterns, want)
	}
	grant := GrantFor(*d.Ask, VerdictAllowRun, "")
	if got := grant.Label(); got != pwNavigate+" to a.example" {
		t.Errorf("grant label %q", got)
	}
	p.Grants = NewGrants(grant)

	confine(t, p)
	d = decideJSON(p, pwNavigate, `{"url":"https://a.example/other"}`)
	if !d.Allow || d.Granted != VerdictAllowRun {
		t.Fatalf("navigate to the granted host: %+v", d)
	}
	if d.Message != "allowed by you for this run (browser not confined to repro hosts)" {
		t.Errorf("granted message %q does not carry the repro reason", d.Message)
	}
	if p.Repro.Confined() {
		t.Error("a grant-allowed off-host navigate left the guard confined")
	}
	if d := decideJSON(p, pwClick, `{"element":"Save"}`); d.Allow {
		t.Error("click after a grant-allowed off-host navigate was auto-allowed")
	}

	d = decideJSON(p, pwNavigate, `{"url":"https://b.example/"}`)
	if d.Allow || d.Ask == nil {
		t.Fatalf("navigate to another host after a grant for a.example: %+v", d)
	}

	// A grant naming the bare tool, as one from before host-scoping
	// would, covers no repro navigate.
	p.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: pwNavigate, Patterns: []string{pwNavigate}})
	if d := decideJSON(p, pwNavigate, `{"url":"https://c.example/"}`); d.Allow {
		t.Error("a bare tool-name grant allowed an off-host navigate")
	}

	// Deny on a.example is not a deny on b.example.
	q := reproPolicy(false)
	d = decideJSON(q, pwNavigate, `{"url":"https://a.example/"}`)
	q.Grants = NewGrants(GrantFor(*d.Ask, VerdictDeny, "no"))
	if d := decideJSON(q, pwNavigate, `{"url":"https://a.example/again"}`); d.Allow || d.Granted != VerdictDeny {
		t.Errorf("denied host: %+v", d)
	}
	if d := decideJSON(q, pwNavigate, `{"url":"https://b.example/"}`); d.Granted != "" || d.Ask == nil {
		t.Errorf("another host after a deny on a.example: %+v, want a fresh question", d)
	}

	// A grant interaction tool shows the repro reason too.
	r := reproPolicy(false)
	r.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: pwClick, Patterns: []string{pwClick}})
	if d := decideJSON(r, pwClick, `{"element":"Save"}`); !d.Allow ||
		d.Message != "allowed by you for this run (browser not confined to repro hosts)" {
		t.Errorf("granted click: %+v", d)
	}
}

// TestReproDryRunsLeaveConfinement: the grant machinery asks "would this
// call be allowed with these patterns" without making it. Those dry runs
// must never reach the guard's decide, which moves confinement.
func TestReproDryRunsLeaveConfinement(t *testing.T) {
	p := reproPolicy(false)
	p.Grants = NewGrants(Grant{Verdict: VerdictAllowRun, Kind: AskMCP, Tool: pwNavigate,
		Patterns: []string{pwNavigate + " host:a.example"}})
	confine(t, p)
	evil := json.RawMessage(`{"url":"https://evil.example/"}`)
	for _, kind := range []string{AskMCP, AskTool, AskFetch, AskBash} {
		p.allowsWith(kind, pwNavigate, evil, []string{"x", pwNavigate + " host:evil.example"})
	}
	ask, ok := p.askFor(pwNavigate, evil)
	if !ok {
		t.Fatal("off-host navigate not askable")
	}
	p.askable(ask, pwNavigate, evil)
	p.Grants.find(p, ask, pwNavigate, evil)
	if !p.Repro.Confined() {
		t.Fatal("a dry run changed confinement")
	}
}

// TestReproRefusedListedHostSaysWhy: a repro host refused for a reason of
// its own — plain http, userinfo — is not "not in repro.hosts".
func TestReproRefusedListedHostSaysWhy(t *testing.T) {
	p := reproPolicy(false)
	d := decideJSON(p, pwNavigate, `{"url":"http://staging.oxo.example/"}`)
	if d.Allow || strings.Contains(d.Message, "is not in repro.hosts") ||
		!strings.HasPrefix(d.Message, "Sirdar policy: browser not confined to repro hosts; ") ||
		!strings.Contains(d.Message, "is http") {
		t.Errorf("http repro host: %q", d.Message)
	}

	d = decideJSON(p, pwNavigate, `{"url":"https://STAGING.oxo.example./"}`)
	if !d.Allow || d.Message != "repro host staging.oxo.example" {
		t.Errorf("trailing-dot host: %+v", d)
	}
}

// TestReproClippedSummaryIsNotTheSameNavigate: two navigates sharing their
// first summaryMax compacted characters share a summary. An allow-for-run
// on the first must not let the second through as "the same call" when it
// goes to another host — by a duplicate key past the cut, or with a
// padding key that pushes url there.
func TestReproClippedSummaryIsNotTheSameNavigate(t *testing.T) {
	long := "https://a.example/" + strings.Repeat("A", 420)
	for _, second := range []string{
		`{"url":"` + long + `","url":"https://evil.example/"}`,
		`{"pad":"` + strings.Repeat("B", 420) + `","url":"https://evil.example/"}`,
	} {
		first := `{"url":"` + long + `"}`
		if strings.HasPrefix(second, `{"pad"`) {
			first = `{"pad":"` + strings.Repeat("B", 420) + `","url":"https://a.example/"}`
		}
		p := reproPolicy(false)
		d := decideJSON(p, pwNavigate, first)
		if d.Ask == nil {
			t.Fatalf("off-host navigate not askable: %+v", d)
		}
		p.Grants = NewGrants(GrantFor(*d.Ask, VerdictAllowRun, ""))
		d = decideJSON(p, pwNavigate, second)
		if d.Allow || d.Ask == nil {
			t.Errorf("navigate to evil.example sharing a clipped summary: got %+v, want a question", d)
		}
		if d := decideJSON(p, pwNavigate, first); !d.Allow {
			t.Errorf("the identical navigate is not matched: %+v", d)
		}
	}
}
