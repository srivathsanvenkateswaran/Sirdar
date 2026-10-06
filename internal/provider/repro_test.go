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
		{"mcp__playwright__browser_run_code", `{"code":"1"}`},
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
