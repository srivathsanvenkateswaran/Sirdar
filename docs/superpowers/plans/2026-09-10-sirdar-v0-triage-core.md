# Sirdar v0 Triage Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Go binary `sirdar` that turns a support ticket key into a Triage note, and later an RCA note plus a Resolution draft, by running one read-only coding-agent session (Claude Code or Codex) inside the workspace, with the ticket fetched host-side.

**Architecture:** `cmd/sirdar` wires subcommands to `internal/run`, which is the only package that depends on everything else. Sources (`internal/source`) fetch tickets into a bundle (`internal/ticket`, `internal/store`). Providers (`internal/provider/{claude,codex}`) spawn the user's installed CLI and normalise its stream into one `Event` type. `internal/prompt` assembles the prompt from embedded preambles, workspace playbooks and embedded JSON schemas; `internal/note` validates the JSON the agent returns and renders markdown through Go templates.

**Tech Stack:** Go 1.24+ (module `github.com/srivathsanvenkateswaran/sirdar`), stdlib `flag` subcommands, `gopkg.in/yaml.v3`, `github.com/santhosh-tekuri/jsonschema/v6`, `text/template`, `os/exec`. No cobra, no Effect-style frameworks. Tests with the standard `testing` package plus golden files under `testdata/`.

**Spec:** `docs/superpowers/specs/2026-09-10-sirdar-v0-triage-core-design.md`. Wire formats verified against the real binaries: `docs/research/06-wire-formats.md`. Executors read all three.

## Global Constraints

- Module path `github.com/srivathsanvenkateswaran/sirdar`; `go 1.24` in `go.mod`; only the two third-party dependencies named above.
- Every package under `internal/` exposes a small interface and hides its I/O; `internal/run` is the only package that imports all the others. `cmd/sirdar` contains command wiring only.
- Triage and rca runs are read-only by construction: the permission policy denies `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, allows only Bash commands matching `permissions.bash` globs, and Codex threads start with `sandbox: "read-only"` and `approvalPolicy: "never"`.
- `ANTHROPIC_API_KEY` is removed from the Claude child environment unless `billing: api`. `--bare` is never passed.
- Credentials (`env:NAME`, `keychain:SERVICE`) are resolved at fetch time, kept in memory, never written to the run directory, never placed in the agent's environment.
- Run directory layout is exactly the spec's: `.sirdar/runs/<KEY>/<run-id>/{bundle/,prompt.md,events.jsonl,result.json,result.raw.txt,note.md,note-resolution.md,playbook-suggestions.md,state.json}`.
- Note frontmatter keys in the default templates: triage `tags,tracker_key,tracker_url,helpdesk_id,helpdesk_url,customer,customer_id,date,priority,service,status,run,provider`; rca adds `date_reported,date_rca,classification,severity,confidence,origin,triage_verdict,related`; resolution `resolution_type,status,pr,pr_status,approved_by,applied_by,applied_at,verified_at,related`.
- Commit messages: plain, imperative, no AI attribution lines of any kind.
- Exit code non-zero if any run ended `failed` or `over_budget`.

---

## File Structure

```
go.mod, go.sum
Makefile                              build, test, lint targets
.github/workflows/ci.yml              go vet + go test on push
cmd/sirdar/main.go                    subcommand dispatch, version
cmd/sirdar/cmd_init.go                init
cmd/sirdar/cmd_doctor.go              doctor
cmd/sirdar/cmd_triage.go              triage
cmd/sirdar/cmd_rca.go                 rca
cmd/sirdar/cmd_resume.go              resume
cmd/sirdar/cmd_runs.go                runs
cmd/sirdar/cmd_register.go            register
internal/config/config.go             Config struct, Load, Validate, defaults
internal/config/creds.go              Resolve("env:X"/"keychain:S"), KeychainReader interface
internal/config/scaffold.go           default config.yaml text for `init`
internal/ticket/ticket.go             TrackerTicket, HelpdeskTicket, Thread, Message, Attachment, Bundle
internal/ticket/bundle.go             WriteBundle(dir, Bundle) -> ticket.json, thread.md, attachments/
internal/source/source.go             Tracker, Helpdesk interfaces, Error{Code,Message}, codes
internal/source/plugin/protocol.go    request/response types for the stdio JSON-lines protocol
internal/source/plugin/client.go      Client: spawn exec, Call, Shutdown; implements Tracker+Helpdesk
internal/source/zohodesk/client.go    Zoho Desk REST client implementing Helpdesk
internal/source/zohodesk/attachments.go  three URL shapes -> download
examples/adapters/file/main.go        reference adapter: tickets from a JSON file (used by tests)
internal/store/run.go                 RunID(), RunDir, State, WriteState/ReadState, ListRuns
internal/store/events.go              EventLog appender (events.jsonl)
internal/store/register.go            Register appender + reader (register.jsonl)
internal/provider/provider.go         SessionSpec, Provider, Session, Event, Result, Budget
internal/provider/policy.go           PermissionPolicy, Decide(tool, input) Decision
internal/provider/claude/claude.go    Provider impl: command line, env, spawn
internal/provider/claude/stream.go    stream-json line decoding -> Event; control_request handling
internal/provider/codex/codex.go      Provider impl: spawn app-server, JSON-RPC client
internal/provider/codex/rpc.go        JSON-RPC framing, request ids, server-request replies
internal/prompt/prompt.go             Assemble(Triage|RCA inputs) -> string
internal/prompt/playbooks.go          Load(dir) []Playbook, Scaffold(dir)
internal/prompt/schemas/triage.json   embedded JSON Schema
internal/prompt/schemas/rca.json      embedded JSON Schema (rca + resolution)
internal/prompt/preamble.md           embedded fixed preamble
internal/note/validate.go             Validate(kind, json) error (jsonschema)
internal/note/render.go               Render(kind, json, meta) -> markdown; templates embedded + override
internal/note/templates/*.md.tmpl     triage, rca, resolution defaults
internal/note/frontmatter.go          UpdateTriageStatus(path, status, links)
internal/note/digest.go               Digest(rows) string
internal/run/run.go                   Runner: Triage(ctx, keys), RCA(ctx, key, opts), Resume(ctx, runID)
internal/run/prepare.go               fetch sources -> bundle -> prompt
internal/run/execute.go               session loop, budgets, event logging, retry on schema failure
internal/run/pool.go                  worker pool with rate-limit pause
docs/adapters.md                      external adapter protocol for adapter authors
```

---

### Task 1: Repository scaffold and CLI skeleton

**Files:**
- Create: `go.mod`, `Makefile`, `.github/workflows/ci.yml`, `cmd/sirdar/main.go`
- Modify: `.gitignore` (add `/sirdar`, `/dist/`, `.sirdar/runs/`)
- Test: `cmd/sirdar/main_test.go`

**Interfaces:**
- Produces: `func run(args []string, stdout, stderr io.Writer) int` in `cmd/sirdar/main.go`; a `commands` map from name to `func(args []string, stdout, stderr io.Writer) int`. Later tasks register `init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register` in that map. `const version = "0.1.0-dev"`.

- [ ] **Step 1: Write the failing test**

```go
// cmd/sirdar/main_test.go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "sirdar ") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr %q", errb.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go mod init github.com/srivathsanvenkateswaran/sirdar && go test ./cmd/sirdar/`
Expected: FAIL, `undefined: run`

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/sirdar/main.go
package main

import (
	"fmt"
	"io"
	"os"
)

const version = "0.1.0-dev"

type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stderr)
		return 2
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "sirdar %s\n", version)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "sirdar: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	return cmd(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: sirdar <command> [flags]

commands:
  init        scaffold .sirdar/config.yaml and playbooks
  doctor      check CLIs, logins, adapters, notes directory
  triage      run triage for one or more ticket keys
  rca         produce the RCA note and Resolution draft
  resume      continue a blocked or interrupted run
  runs        list runs and states
  register    print the register
  version     print version`)
}
```

`Makefile`:

```make
.PHONY: build test vet
build: ; go build -o sirdar ./cmd/sirdar
test:  ; go test ./...
vet:   ; go vet ./...
```

`.github/workflows/ci.yml`:

```yaml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.24' }
      - run: go vet ./...
      - run: go test ./...
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/sirdar/ && go build ./... && ./sirdar version` (after `make build`)
Expected: PASS; prints `sirdar 0.1.0-dev`

- [ ] **Step 5: Commit**

```bash
git add go.mod Makefile .github cmd .gitignore
git commit -m "Scaffold module, CLI dispatch, and CI"
```

---

### Task 2: Configuration loading and credential references

**Files:**
- Create: `internal/config/config.go`, `internal/config/creds.go`, `internal/config/scaffold.go`
- Test: `internal/config/config_test.go`, `internal/config/creds_test.go`

**Interfaces:**
- Produces:

```go
package config

type Provider string // "claude" | "codex"

type SourceConfig struct {
	Adapter string            `yaml:"adapter"` // "exec" | "zohodesk"
	Command string            `yaml:"command,omitempty"`
	OrgID   string            `yaml:"orgId,omitempty"`
	BaseURL string            `yaml:"baseUrl,omitempty"`
	Token   string            `yaml:"token,omitempty"` // credential ref
	Extra   map[string]string `yaml:",inline"`
}

type Config struct {
	Workspace   string   `yaml:"workspace"`
	Provider    Provider `yaml:"provider"`
	Model       string   `yaml:"model"`
	Billing     string   `yaml:"billing"` // "subscription" | "api"
	Sources     struct {
		Tracker  *SourceConfig `yaml:"tracker"`
		Helpdesk *SourceConfig `yaml:"helpdesk"`
	} `yaml:"sources"`
	Notes struct {
		Dir       string `yaml:"dir"`
		Templates string `yaml:"templates"`
		Filenames struct {
			Triage     string `yaml:"triage"`
			RCA        string `yaml:"rca"`
			Resolution string `yaml:"resolution"`
		} `yaml:"filenames"`
	} `yaml:"notes"`
	Budget struct {
		MaxTurns   int     `yaml:"maxTurns"`
		MaxMinutes int     `yaml:"maxMinutes"`
		MaxUSD     float64 `yaml:"maxUsd"`
	} `yaml:"budget"`
	Concurrency int `yaml:"concurrency"`
	Permissions struct {
		Bash []string `yaml:"bash"`
	} `yaml:"permissions"`
	Playbooks string `yaml:"playbooks"`
	Providers struct {
		Claude struct{ Path string `yaml:"path"` } `yaml:"claude"`
		Codex  struct{ Path string `yaml:"path"` } `yaml:"codex"`
	} `yaml:"providers"`

	Root string `yaml:"-"` // workspace root (directory containing .sirdar), set by Load
}

// Load reads <root>/.sirdar/config.yaml, applies defaults, validates.
func Load(root string) (*Config, error)
// FindRoot walks up from dir to the first directory containing .sirdar/config.yaml.
func FindRoot(dir string) (string, error)
func (c *Config) Validate() error
// ExpandPath expands a leading ~ and makes relative paths relative to c.Root.
func (c *Config) ExpandPath(p string) string

// creds.go
type KeychainReader interface{ Read(service string) (string, error) }
type Resolver struct {
	Env      func(string) (string, bool)
	Keychain KeychainReader
}
func (r Resolver) Resolve(ref string) (string, error) // "env:NAME" | "keychain:SERVICE" | literal is an error
type MacKeychain struct{}                              // runs `security find-generic-password -s SERVICE -w`

// scaffold.go
const DefaultConfigYAML string // the spec's example with placeholders
```

Defaults: provider `claude`, billing `subscription`, filenames `"{key} {slug}.md"`, `"{key} RCA {slug}.md"`, `"{key} RES {slug}.md"`, budget 60 turns / 25 minutes / 5 USD, concurrency 1, playbooks `.sirdar/playbooks`, notes.dir `.sirdar/notes`.

Validation rules (each produces an error naming the key): `provider` in {claude,codex}; `billing` in {subscription,api}; a source with adapter `exec` needs `command`; adapter `zohodesk` needs `orgId`, `baseUrl`, `token`; unknown adapter is an error; `concurrency >= 1`; every budget value `> 0`; unknown top-level YAML keys are errors (use `yaml.Decoder.KnownFields(true)`); token refs must start with `env:` or `keychain:`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755)
	os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644)
	return root
}

const minimal = `
workspace: demo
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.example
    token: env:ZOHO
`

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil { t.Fatal(err) }
	if c.Provider != "claude" || c.Billing != "subscription" || c.Concurrency != 1 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.Budget.MaxTurns != 60 || c.Budget.MaxMinutes != 25 || c.Budget.MaxUSD != 5 {
		t.Fatalf("budget defaults: %+v", c.Budget)
	}
	if c.Notes.Filenames.RCA != "{key} RCA {slug}.md" { t.Fatalf("filename default %q", c.Notes.Filenames.RCA) }
	if c.Playbooks != ".sirdar/playbooks" { t.Fatalf("playbooks default %q", c.Playbooks) }
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"\nbogus: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus") { t.Fatalf("want unknown-key error, got %v", err) }
}

func TestValidateExecNeedsCommand(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"  tracker:\n    adapter: exec\n"))
	if err == nil || !strings.Contains(err.Error(), "sources.tracker.command") { t.Fatalf("got %v", err) }
}

func TestValidateBadProvider(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"provider: gemini\n"))
	if err == nil || !strings.Contains(err.Error(), "provider") { t.Fatalf("got %v", err) }
}

func TestValidateTokenRef(t *testing.T) {
	bad := strings.Replace(minimal, "env:ZOHO", "plaintext-token", 1)
	_, err := Load(writeCfg(t, bad))
	if err == nil || !strings.Contains(err.Error(), "token") { t.Fatalf("got %v", err) }
}

func TestFindRoot(t *testing.T) {
	root := writeCfg(t, minimal)
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	got, err := FindRoot(nested)
	if err != nil || got != root { t.Fatalf("got %q err %v", got, err) }
	if _, err := FindRoot(t.TempDir()); err == nil { t.Fatal("expected error outside a workspace") }
}

func TestExpandPath(t *testing.T) {
	c := &Config{Root: "/ws"}
	home, _ := os.UserHomeDir()
	if got := c.ExpandPath("~/x"); got != filepath.Join(home, "x") { t.Fatal(got) }
	if got := c.ExpandPath("rel/y"); got != "/ws/rel/y" { t.Fatal(got) }
	if got := c.ExpandPath("/abs"); got != "/abs" { t.Fatal(got) }
}
```

```go
// internal/config/creds_test.go
package config

import "testing"

type fakeKC map[string]string

func (f fakeKC) Read(s string) (string, error) {
	v, ok := f[s]
	if !ok { return "", &NotFoundError{Ref: "keychain:" + s} }
	return v, nil
}

func TestResolve(t *testing.T) {
	r := Resolver{
		Env:      func(k string) (string, bool) { if k == "ZOHO" { return "e-tok", true }; return "", false },
		Keychain: fakeKC{"zoho": "k-tok"},
	}
	if v, _ := r.Resolve("env:ZOHO"); v != "e-tok" { t.Fatal(v) }
	if v, _ := r.Resolve("keychain:zoho"); v != "k-tok" { t.Fatal(v) }
	if _, err := r.Resolve("env:MISSING"); err == nil { t.Fatal("want error") }
	if _, err := r.Resolve("keychain:nope"); err == nil { t.Fatal("want error") }
	if _, err := r.Resolve("literal"); err == nil { t.Fatal("literal refs are rejected") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL, undefined symbols

- [ ] **Step 3: Implement**

`config.go`: `Load` reads the file, decodes with `yaml.NewDecoder(f)` and `dec.KnownFields(true)` into `Config`, sets `Root`, calls `applyDefaults`, then `Validate`. `FindRoot` loops `dir` upward until `filepath.Dir(dir) == dir`. `ExpandPath` handles `~/`, absolute, relative-to-Root. `Validate` collects rules above; return `fmt.Errorf("config: %s: %s", key, problem)`.

`creds.go`:

```go
type NotFoundError struct{ Ref string }
func (e *NotFoundError) Error() string { return "credential not found: " + e.Ref }

func (r Resolver) Resolve(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		if r.Env == nil { r.Env = os.LookupEnv }
		v, ok := r.Env(ref[4:])
		if !ok || v == "" { return "", &NotFoundError{Ref: ref} }
		return v, nil
	case strings.HasPrefix(ref, "keychain:"):
		if r.Keychain == nil { return "", fmt.Errorf("keychain refs are not supported on this platform") }
		return r.Keychain.Read(ref[len("keychain:"):])
	}
	return "", fmt.Errorf("credential ref %q must start with env: or keychain:", ref)
}

func (MacKeychain) Read(service string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil { return "", &NotFoundError{Ref: "keychain:" + service} }
	return strings.TrimSpace(string(out)), nil
}
```

`scaffold.go`: `DefaultConfigYAML` is the spec's example with `workspace: <name>`, placeholders `<org-id>`, `keychain:<service>`, `~/bin/<adapter>`; comments explain each key.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config go.mod go.sum
git commit -m "Add workspace config loading, validation, and credential refs"
```

---

### Task 3: Ticket types and bundle writer

**Files:**
- Create: `internal/ticket/ticket.go`, `internal/ticket/bundle.go`
- Test: `internal/ticket/bundle_test.go`, `internal/ticket/testdata/thread.golden.md`

**Interfaces:**
- Produces:

```go
package ticket

type Role string // "customer" | "agent" | "system"

type TrackerTicket struct {
	Key, Title, Description, Priority, Status, Assignee, URL string
	HelpdeskRef string // helpdesk ticket id parsed from the tracker record; "" if none
	CreatedAt, UpdatedAt time.Time
	Fields map[string]string // adapter-specific extras (e.g. epic, company)
}

type HelpdeskTicket struct {
	ID, Subject, Status, Priority, Channel, Contact, Customer, CustomerID, URL string
	CreatedAt, UpdatedAt time.Time
	Fields map[string]string
}

type Message struct {
	At            time.Time
	Author        string
	Role          Role
	Text          string
	AttachmentIDs []string
}
type Thread []Message

type Attachment struct{ ID, Name, MIME, Path string }

type Bundle struct {
	Tracker     *TrackerTicket  // nil when the workspace has no tracker
	Helpdesk    *HelpdeskTicket // nil when fetch failed or absent
	Thread      Thread
	Attachments []Attachment
	Warnings    []string // e.g. attachment download failures, surfaced to the prompt
}

// WriteBundle writes ticket.json, thread.md and copies nothing (attachments are already
// in dir/attachments because Helpdesk.Attachments downloaded them there).
func WriteBundle(dir string, b Bundle) error
// ThreadMarkdown renders the thread in original language with roles and RFC3339 times.
func ThreadMarkdown(t Thread, atts []Attachment) string
// Key returns Tracker.Key if present else Helpdesk.ID.
func (b Bundle) Key() string
```

- [ ] **Step 1: Write the failing test**

```go
// internal/ticket/bundle_test.go
package ticket

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sample() Bundle {
	t0 := time.Date(2026, 9, 10, 8, 30, 0, 0, time.FixedZone("KSA", 3*3600))
	return Bundle{
		Tracker:  &TrackerTicket{Key: "OMNI-1", Title: "Export fails", HelpdeskRef: "555", URL: "https://t/OMNI-1"},
		Helpdesk: &HelpdeskTicket{ID: "555", Subject: "تصدير", Customer: "شركة", CustomerID: "4561", URL: "https://h/555"},
		Thread: Thread{
			{At: t0, Author: "Customer", Role: "customer", Text: "لا يعمل التصدير", AttachmentIDs: []string{"a1"}},
			{At: t0.Add(time.Hour), Author: "L1", Role: "agent", Text: "سنتحقق"},
		},
		Attachments: []Attachment{{ID: "a1", Name: "shot.png", MIME: "image/png", Path: "attachments/1-shot.png"}},
	}
}

func TestThreadMarkdownGolden(t *testing.T) {
	got := ThreadMarkdown(sample().Thread, sample().Attachments)
	want, _ := os.ReadFile("testdata/thread.golden.md")
	if os.Getenv("UPDATE_GOLDEN") != "" { os.WriteFile("testdata/thread.golden.md", []byte(got), 0o644); return }
	if got != string(want) { t.Fatalf("golden mismatch:\n%s", got) }
}

func TestWriteBundle(t *testing.T) {
	dir := t.TempDir()
	if err := WriteBundle(dir, sample()); err != nil { t.Fatal(err) }
	raw, err := os.ReadFile(filepath.Join(dir, "ticket.json"))
	if err != nil { t.Fatal(err) }
	var back Bundle
	if err := json.Unmarshal(raw, &back); err != nil { t.Fatal(err) }
	if back.Tracker.Key != "OMNI-1" || back.Helpdesk.CustomerID != "4561" { t.Fatalf("%+v", back) }
	if _, err := os.Stat(filepath.Join(dir, "thread.md")); err != nil { t.Fatal(err) }
	if sample().Key() != "OMNI-1" { t.Fatal("key") }
	if (Bundle{Helpdesk: &HelpdeskTicket{ID: "9"}}).Key() != "9" { t.Fatal("helpdesk key fallback") }
}
```

`testdata/thread.golden.md` (exact expected output):

```markdown
# Conversation (original language)

## 2026-09-10T08:30:00+03:00 · customer · Customer

لا يعمل التصدير

Attachments: attachments/1-shot.png

## 2026-09-10T09:30:00+03:00 · agent · L1

سنتحقق

```

(The file ends with a single newline after the last message block.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ticket/`
Expected: FAIL

- [ ] **Step 3: Implement** `ThreadMarkdown` (header, one `##` per message with `At.Format(time.RFC3339) · role · author`, blank line, text verbatim, optional `Attachments:` line listing the `Path` of each attachment whose ID is in `AttachmentIDs`, blank line) and `WriteBundle` (`os.MkdirAll`, `json.MarshalIndent(b, "", "  ")` to `ticket.json`, `thread.md`).

- [ ] **Step 4: Run tests** — `go test ./internal/ticket/` → PASS

- [ ] **Step 5: Commit** — `git add internal/ticket && git commit -m "Add ticket types and bundle writer"`

---

### Task 4: Source interfaces and the external adapter protocol

**Files:**
- Create: `internal/source/source.go`, `internal/source/plugin/protocol.go`, `internal/source/plugin/client.go`, `examples/adapters/file/main.go`, `docs/adapters.md`
- Test: `internal/source/plugin/client_test.go`, `internal/source/plugin/testdata/tickets.json`

**Interfaces:**
- Consumes: `ticket.*` types from Task 3.
- Produces:

```go
package source

type Code string
const (
	NotFound    Code = "not_found"
	Auth        Code = "auth"
	Unsupported Code = "unsupported"
	RateLimited Code = "rate_limited"
	Internal    Code = "internal"
)
type Error struct{ Code Code; Message string }
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

type ListFilter struct{ Assignee, Status, Parent string; Limit int }

type Tracker interface {
	Get(ctx context.Context, key string) (ticket.TrackerTicket, error)
	List(ctx context.Context, f ListFilter) ([]ticket.TrackerTicket, error)
}
type Helpdesk interface {
	Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error)
	Threads(ctx context.Context, id string) (ticket.Thread, error)
	Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error)
}
type Closer interface{ Close() error }
```

```go
package plugin

// Wire: one JSON object per line on stdin (requests) and stdout (responses).
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"` // describe | tracker.get | tracker.list | helpdesk.get | helpdesk.threads | helpdesk.attachments | shutdown
	Params json.RawMessage `json:"params,omitempty"`
}
type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *source.Error   `json:"error,omitempty"`
}
type Describe struct{ Name string `json:"name"`; Roles []string `json:"roles"`; Version string `json:"version"` }

type Client struct{ /* cmd, stdin, stdout scanner, mutex, next id, stderr buffer */ }
// Start spawns command (split with shell-style quoting by strings.Fields is NOT enough; use
// a small splitter that honours double quotes) with env inherited minus nothing added.
func Start(ctx context.Context, command string, stderr io.Writer) (*Client, error)
func (c *Client) Describe(ctx context.Context) (Describe, error)
func (c *Client) Close() error // sends shutdown, waits 5s, then kills
// Client implements source.Tracker and source.Helpdesk; methods return *source.Error{Unsupported}
// when Describe.Roles lacks the role.
```

Attachment params: `{"id":"555","dir":"/abs/path/attachments"}`; result: `[]ticket.Attachment` with `Path` relative to the bundle dir (`attachments/1-shot.png`).

`examples/adapters/file/main.go`: a standalone program (`package main`) that reads a JSON file given by `-file` (or env `SIRDAR_FILE_ADAPTER`) with shape `{"tracker":{"OMNI-1":{...TrackerTicket...}},"helpdesk":{"555":{"ticket":{...},"thread":[...],"attachments":[{"id","name","mime","content_base64"}]}}}` and serves the protocol; on `helpdesk.attachments` it writes the decoded files into `dir` as `<index>-<name>` and returns their metadata. It reports both roles. It is the adapter used in tests and in CI's `--dry-run`.

- [ ] **Step 1: Write the failing test**

```go
// internal/source/plugin/client_test.go
package plugin

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

func buildFileAdapter(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "file-adapter")
	cmd := exec.Command("go", "build", "-o", bin, "../../../examples/adapters/file")
	if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("build: %v\n%s", err, out) }
	return bin
}

func TestClientRoundTrip(t *testing.T) {
	bin := buildFileAdapter(t)
	var stderr bytes.Buffer
	c, err := Start(context.Background(), bin+` -file "testdata/tickets.json"`, &stderr)
	if err != nil { t.Fatal(err) }
	defer c.Close()

	d, err := c.Describe(context.Background())
	if err != nil || d.Name != "file" || len(d.Roles) != 2 { t.Fatalf("%+v %v", d, err) }

	tt, err := c.Get(context.Background(), "OMNI-1")
	if err != nil || tt.Title != "Export fails" || tt.HelpdeskRef != "555" { t.Fatalf("%+v %v", tt, err) }

	if _, err := c.Get(context.Background(), "OMNI-404"); err == nil {
		t.Fatal("want not_found")
	} else if se, ok := err.(*source.Error); !ok || se.Code != source.NotFound { t.Fatalf("got %v", err) }

	th, err := c.Threads(context.Background(), "555")
	if err != nil || len(th) != 2 || th[0].Role != "customer" { t.Fatalf("%+v %v", th, err) }

	dir := t.TempDir()
	atts, err := c.Attachments(context.Background(), "555", filepath.Join(dir, "attachments"))
	if err != nil || len(atts) != 1 { t.Fatalf("%+v %v", atts, err) }
	if _, err := os.Stat(filepath.Join(dir, "attachments", "1-shot.png")); err != nil { t.Fatal(err) }
	if atts[0].Path != "attachments/1-shot.png" { t.Fatal(atts[0].Path) }
}

func TestClientShutdownTimeout(t *testing.T) {
	// A process that ignores shutdown must be killed within ~5s.
	c, err := Start(context.Background(), "sleep 60", &bytes.Buffer{})
	if err != nil { t.Fatal(err) }
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Close did not return")
	}
}
```

`testdata/tickets.json`:

```json
{
  "tracker": {"OMNI-1": {"Key":"OMNI-1","Title":"Export fails","Description":"Zoho Ticket URL: https://desk.example/tickets/555","Priority":"high","Status":"open","URL":"https://t/OMNI-1","HelpdeskRef":"555"}},
  "helpdesk": {"555": {
    "ticket": {"ID":"555","Subject":"تصدير","Customer":"شركة","CustomerID":"4561","URL":"https://h/555"},
    "thread": [
      {"At":"2026-09-10T08:30:00+03:00","Author":"Customer","Role":"customer","Text":"لا يعمل التصدير","AttachmentIDs":["a1"]},
      {"At":"2026-09-10T09:30:00+03:00","Author":"L1","Role":"agent","Text":"سنتحقق"}
    ],
    "attachments": [{"id":"a1","name":"shot.png","mime":"image/png","content_base64":"iVBORw0KGgo="}]
  }}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/source/...` → FAIL

- [ ] **Step 3: Implement** `source.go` as above; `protocol.go` types; `client.go`:

```go
func Start(ctx context.Context, command string, stderr io.Writer) (*Client, error) {
	argv := splitCommand(command) // honours "double quoted" segments
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil { return nil, fmt.Errorf("start adapter: %w", err) }
	return &Client{cmd: cmd, in: stdin, out: bufio.NewScanner(stdout)}, nil
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock(); defer c.mu.Unlock()
	c.next++
	req := Request{ID: c.next, Method: method}
	if params != nil { req.Params, _ = json.Marshal(params) }
	b, _ := json.Marshal(req)
	if _, err := c.in.Write(append(b, '\n')); err != nil { return &source.Error{Code: source.Internal, Message: err.Error()} }
	for c.out.Scan() {
		var resp Response
		if err := json.Unmarshal(c.out.Bytes(), &resp); err != nil { continue } // tolerate noise
		if resp.ID != req.ID { continue }
		if resp.Error != nil { return resp.Error }
		if result != nil { return json.Unmarshal(resp.Result, result) }
		return nil
	}
	return &source.Error{Code: source.Internal, Message: "adapter closed stdout"}
}
```

`Close`: write `{"id":N,"method":"shutdown"}`, close stdin, `cmd.Wait()` in a goroutine, `select` with `time.After(5*time.Second)` then `cmd.Process.Kill()`. Set the scanner buffer to 16 MiB (`scanner.Buffer(make([]byte, 1<<20), 16<<20)`) so large thread payloads fit.

The role check: `Describe` is called lazily once and cached; `Get`/`List` return `Unsupported` if `"tracker"` missing; helpdesk methods likewise.

`examples/adapters/file/main.go`: read requests with a scanner, dispatch on method, write responses, exit on `shutdown` or EOF.

`docs/adapters.md`: the protocol as in the spec, with the file adapter as the worked example.

- [ ] **Step 4: Run tests** — `go test ./internal/source/... ./examples/...` → PASS

- [ ] **Step 5: Commit** — `git add internal/source examples docs/adapters.md && git commit -m "Add source interfaces, stdio adapter protocol, and file adapter"`

---

### Task 5: Zoho Desk helpdesk adapter

**Files:**
- Create: `internal/source/zohodesk/client.go`, `internal/source/zohodesk/attachments.go`
- Test: `internal/source/zohodesk/client_test.go`, `internal/source/zohodesk/testdata/{ticket.json,conversations.json,thread.json}`

**Interfaces:**
- Consumes: `source.Helpdesk`, `ticket.*`.
- Produces:

```go
package zohodesk

type Client struct{ BaseURL, OrgID, Token string; HTTP *http.Client }
func New(baseURL, orgID, token string) *Client
// implements source.Helpdesk
```

HTTP calls (Zoho Desk v1): `GET {base}/api/v1/tickets/{id}` with headers `orgId: {org}` and `Authorization: Zoho-oauthtoken {token}`; `GET /api/v1/tickets/{id}/conversations?limit=100` (paginate with `from`); for each conversation of type `thread`, `GET /api/v1/tickets/{id}/threads/{threadId}?include=plainText`; for type `comment`, use the comment body. Attachments: collect from thread `attachments[]` (`href` or `/api/v1/tickets/{id}/attachments/{aid}/content`), inline `<img src="…/inlineattachments/…">` in HTML content, and IM sessions (`/api/v1/im/sessions/...`); download each with the same headers into `dir/<index>-<name>`. Map: `Role` = `customer` when `direction == "in"` or author type is contact, `agent` otherwise; `Author` from `author.name`; `At` parsed from `createdTime` (RFC3339). HTTP 401/403 → `source.Error{Auth}`, 404 → `NotFound`, 429 → `RateLimited`, other non-2xx → `Internal` with status and first 200 bytes of body.

- [ ] **Step 1: Write the failing tests** using `httptest.NewServer` serving the fixtures at those paths, asserting headers, mapped fields, two messages (one from a thread, one from a comment), and three downloaded attachments named `1-…`, `2-…`, `3-…` from the three URL shapes; plus a 401 test → `source.Auth` and a 429 test → `source.RateLimited`. Fixture JSON: minimal objects with only the fields the client reads (`id`, `subject`, `status`, `priority`, `channel`, `contact.lastName`, `accountName`/`cf.cf_company_id`, `createdTime`, `webUrl`; conversations `data[]` with `type`, `id`, `direction`, `author.name`, `createdTime`, `content`, `attachments[]`).

- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add Zoho Desk helpdesk adapter"`

---

### Task 6: Run store, event log, register

**Files:**
- Create: `internal/store/run.go`, `internal/store/events.go`, `internal/store/register.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:

```go
package store

type Kind string   // "triage" | "rca"
type Status string // "preparing" | "running" | "completed" | "failed" | "blocked" | "over_budget"

type Usage struct{ Turns int; InputTokens, OutputTokens int64; CostUSD float64 }

type State struct {
	RunID, Key string
	Kind Kind
	Status Status
	Provider, Model string
	Handle string          // provider session/thread id for resume
	Reason string          // failure/blocked reason
	StartedAt, UpdatedAt time.Time
	Usage Usage
	Budget struct{ MaxTurns, MaxMinutes int; MaxUSD float64 }
	Notes []string         // paths written
	StderrTail []string
	Warnings []string
}

func NewRunID(now time.Time) string // "20260910T113000Z-ab12"
type Run struct{ Dir string }        // .sirdar/runs/<KEY>/<run-id>
func Create(root, key string, now time.Time) (Run, error)  // mkdir bundle/, bundle/attachments/
func Open(root, runID string) (Run, State, error)          // finds the run dir by id under any key
func (r Run) BundleDir() string
func (r Run) WriteState(s State) error   // atomic: write tmp, rename
func (r Run) ReadState() (State, error)
func List(root, key string) ([]State, error) // key=="" lists all, newest first
func LatestNote(root, key string, kind Kind) (string, error) // path of newest completed run's note.md

type EventLog struct{ /* file */ }
func (r Run) OpenEventLog() (*EventLog, error)
func (l *EventLog) Append(kind string, payload any) error // {"t":"2026-..","kind":"...","payload":{...}}
func (l *EventLog) Close() error

type RegisterRow struct {
	Key string; Kind string; RunID string; Date string; Provider, Model, Service, Classification, Confidence, Severity string
	Turns int; CostUSD float64; TriageVerdict string; NotePath string
}
func AppendRegister(root string, row RegisterRow) error
func ReadRegister(root string) ([]RegisterRow, error)
```

- [ ] **Step 1: Write the failing tests**: `NewRunID` is sortable and unique across two calls one second apart; `Create` makes the directories; `WriteState`/`ReadState` round-trip; `Open` finds a run by id; `List` orders newest first and filters by key; `EventLog.Append` writes one JSON line with `t`, `kind`, `payload`; `AppendRegister` then `ReadRegister` round-trips two rows; `LatestNote` picks the newest completed run.

- [ ] **Step 2–4:** FAIL → implement (state via `json.MarshalIndent`, tmp+`os.Rename`) → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add run store, event log, and register"`

---

### Task 7: Provider contracts and permission policy

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/policy.go`
- Test: `internal/provider/policy_test.go`

**Interfaces:**
- Produces:

```go
package provider

type Budget struct{ MaxTurns, MaxMinutes int; MaxUSD float64 }

type SessionSpec struct {
	Cwd, Prompt, Model string
	OutputSchema []byte
	Policy       *PermissionPolicy
	Budget       Budget
	Resume       string
	Images       []string
	Env          []string // full child env; provider may strip keys
	Binary       string   // path override; "" = look up on PATH
}

type EventKind string
const (
	EvAssistantText EventKind = "assistant_text"
	EvToolStarted   EventKind = "tool_started"
	EvToolFinished  EventKind = "tool_finished"
	EvPermission    EventKind = "permission"
	EvUsage         EventKind = "usage"
	EvRateLimited   EventKind = "rate_limited"
	EvQuestion      EventKind = "question"
	EvFinal         EventKind = "final"
	EvSystem        EventKind = "system"   // init, status, anything informational
	EvError         EventKind = "error"
)

type Event struct {
	Kind      EventKind
	At        time.Time
	Text      string            // assistant text / question / error message
	Tool      string            // tool name for tool_* and permission
	Input     json.RawMessage   // tool input
	Decision  string            // "allow" | "deny" for permission
	Turns     int
	InputTok, OutputTok int64
	CostUSD   float64
	ResetsAt  time.Time         // rate limit
	Final     json.RawMessage   // structured output, nil if absent
	Raw       json.RawMessage   // original provider line, always set
}

type Result struct {
	Final    json.RawMessage
	Text     string  // final text when no structured output
	Usage    struct{ Turns int; InputTok, OutputTok int64; CostUSD float64 }
	Handle   string
	ExitErr  error   // non-nil if the process failed
}

type Session interface {
	Events() <-chan Event
	Send(ctx context.Context, userText string) error // follow-up user message (schema retry)
	Wait() (Result, error)
	Handle() string
	Cancel()
}

type Check struct{ Name string; OK bool; Detail string }

type Provider interface {
	Name() string
	Start(ctx context.Context, spec SessionSpec) (Session, error)
	Doctor(ctx context.Context, binary string) []Check
}
```

```go
// policy.go
type Decision struct{ Allow bool; Message string }
type PermissionPolicy struct{ BashAllow []string }
func (p *PermissionPolicy) Decide(tool string, input json.RawMessage) Decision
func MatchGlob(pattern, command string) bool // '*' matches any run incl. spaces and '/', '?' one char; case-sensitive
```

Rules exactly as the spec table: `Read, Glob, Grep, LS, WebFetch, WebSearch, TodoWrite, Task, StructuredOutput` and any name starting with `mcp__` → allow; `Edit, Write, MultiEdit, NotebookEdit` → deny "Sirdar policy: triage runs are read-only"; `Bash` → extract `input.command`, trim, allow if any glob matches else deny with message `"Sirdar policy: command not in the allow-list (" + strings.Join(p.BashAllow, ", ") + ")"`; unknown → deny "Sirdar policy: tool <name> is not permitted".

- [ ] **Step 1: Write the failing test**

```go
func TestPolicy(t *testing.T) {
	p := &PermissionPolicy{BashAllow: []string{"git log*", "rg *", "dotnet build*"}}
	cases := []struct{ tool, input string; allow bool }{
		{"Read", `{"file_path":"x"}`, true},
		{"mcp__grafana__query", `{}`, true},
		{"Write", `{"file_path":"x","content":"y"}`, false},
		{"Bash", `{"command":"git log --oneline -5"}`, true},
		{"Bash", `{"command":"  rg foo src/ "}`, true},
		{"Bash", `{"command":"rm -rf /"}`, false},
		{"Bash", `{"command":"git logs"}`, true},   // glob "git log*" matches "git logs"
		{"Bash", `{"command":"dotnet test"}`, false},
		{"Unknown", `{}`, false},
	}
	for _, c := range cases {
		d := p.Decide(c.tool, json.RawMessage(c.input))
		if d.Allow != c.allow { t.Errorf("%s %s: got allow=%v msg=%q", c.tool, c.input, d.Allow, d.Message) }
		if !d.Allow && d.Message == "" { t.Errorf("deny without message for %s", c.tool) }
	}
}

func TestMatchGlob(t *testing.T) {
	if !MatchGlob("git show*", "git show HEAD:path/file.cs") { t.Fatal("slash in run") }
	if MatchGlob("git show*", "gitshow") { t.Fatal("space is literal") }
	if !MatchGlob("?cho hi", "echo hi") { t.Fatal("?") }
}
```

- [ ] **Step 2–4:** FAIL → implement (`MatchGlob` via regexp built from `regexp.QuoteMeta` with `\*`→`.*`, `\?`→`.`, anchored) → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add provider contracts and read-only permission policy"`

---

### Task 8: Claude Code provider (stream-json + control protocol)

**Files:**
- Create: `internal/provider/claude/claude.go`, `internal/provider/claude/stream.go`
- Test: `internal/provider/claude/claude_test.go`, `internal/provider/claude/testdata/script-basic.jsonl`

**Interfaces:**
- Consumes: `provider.*` from Task 7; wire shapes from `docs/research/06-wire-formats.md`.
- Produces: `func New() provider.Provider` (Name `"claude"`).

Command line built by `args(spec)`:

```
<binary> -p --output-format stream-json --input-format stream-json --verbose
  --include-partial-messages --permission-prompt-tool stdio --permission-mode default
  --json-schema <spec.OutputSchema as one string>
  [--model spec.Model] [--max-turns spec.Budget.MaxTurns] [--resume spec.Resume]
  --disallowedTools "Write,Edit,MultiEdit,NotebookEdit"
```

Environment: `spec.Env` with `ANTHROPIC_API_KEY=` entries removed unless `spec.Env` contains `SIRDAR_BILLING=api` (the runner sets that marker from config). `--bare` never.

Session behaviour:
- After start, write `{"type":"user","message":{"role":"user","content":<Prompt>}}\n` to stdin. Keep stdin open.
- Reader goroutine: `bufio.Scanner` (16 MiB buffer) over stdout; each line → `decode(line) []provider.Event` and, for `control_request` with `subtype == "can_use_tool"`, `spec.Policy.Decide(tool_name, input)` → write `control_response` (allow → `{"behavior":"allow","updatedInput":<input>}`; deny → `{"behavior":"deny","message":<msg>}`), emit `EvPermission`. Any other `control_request` subtype → reply deny with message `"unsupported control request"` and emit `EvSystem`.
- Decode mapping: `system/init` → `EvSystem` and record `session_id` as handle; `assistant` with `tool_use` blocks → `EvToolStarted` (Tool, Input); `assistant` with `text` blocks → `EvAssistantText`; `user` with `tool_result` → `EvToolFinished`; `rate_limit_event` → `EvRateLimited` with `ResetsAt` from `rate_limit_info.resetsAt` (unix seconds) and Text `rateLimitType`; `result` → `EvFinal` with `Final = structured_output` (nil when absent), `Text = result`, `Turns = num_turns`, tokens from `usage`, `CostUSD = total_cost_usd`; also emit `EvUsage` before `EvFinal`. `stream_event`, `system/*` other → `EvSystem`. Malformed line → `EvError{Text:"malformed line"}` (the runner counts them).
- `Send(userText)` writes another `user` line (used for the schema-retry turn); the CLI then produces another `result`.
- `Wait()` waits for process exit; `Result.Handle` = session id; `ExitErr` set if exit code ≠ 0; stderr collected into a 50-line tail available via `Result.ExitErr` message.
- `Cancel()` sends SIGINT, waits 10s, then SIGKILL.
- `Doctor(binary)`: run `<binary> --version` (expect prefix digit), and `<binary> auth status` if available (best effort: report `OK=false, Detail` on non-zero, but do not fail on unknown subcommand).

**Fake binary for tests.** Use the re-exec pattern: `TestMain` checks `os.Getenv("SIRDAR_FAKE_CLAUDE")`; when set, the test binary acts as the fake CLI: it reads the script file named by the env var line by line and writes each line to stdout; a script line `{"$wait":"control_response"}` makes it block reading stdin until a line containing `"control_response"` arrives, then continue; `{"$wait":"user"}` blocks until a `"type":"user"` line arrives; `{"$exit":1}` exits with that code; `{"$stderr":"boom"}` writes to stderr. Tests set `spec.Binary = os.Args[0]` and put `SIRDAR_FAKE_CLAUDE=<script>` in `spec.Env`.

`testdata/script-basic.jsonl` (abridged from the verified capture; the executor copies the exact shapes from `06-wire-formats.md`):

```
{"type":"system","subtype":"init","cwd":"/w","session_id":"sess-1","tools":["Bash","Read"],"model":"m","permissionMode":"default"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"/w/x","content":"y"}}]}}
{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/w/x","content":"y"},"tool_use_id":"t1"}}
{"$wait":"control_response"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":"denied","is_error":true}]}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1789049400,"rateLimitType":"five_hour"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"git log -1"}}]}}
{"type":"control_request","request_id":"r2","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"git log -1"},"tool_use_id":"t2"}}
{"$wait":"control_response"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t2","type":"tool_result","content":"abc123","is_error":false}]}}
{"type":"result","subtype":"success","is_error":false,"num_turns":3,"session_id":"sess-1","result":"{\"greeting\":\"hi\",\"n\":7}","structured_output":{"greeting":"hi","n":7},"total_cost_usd":0.07,"usage":{"input_tokens":18,"output_tokens":516}}
```

- [ ] **Step 1: Write the failing tests**

```go
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_CLAUDE"); script != "" { os.Exit(fakeCLI(script)) }
	os.Exit(m.Run())
}

func TestBasicSession(t *testing.T) {
	p := New()
	spec := provider.SessionSpec{
		Cwd: t.TempDir(), Prompt: "hello", OutputSchema: []byte(`{"type":"object"}`),
		Policy: &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
		Binary: os.Args[0],
		Env: append(os.Environ(), "SIRDAR_FAKE_CLAUDE=testdata/script-basic.jsonl", "ANTHROPIC_API_KEY=should-be-stripped"),
	}
	s, err := p.Start(context.Background(), spec)
	if err != nil { t.Fatal(err) }
	var perms []provider.Event
	var final *provider.Event
	var rl bool
	for ev := range s.Events() {
		switch ev.Kind {
		case provider.EvPermission: perms = append(perms, ev)
		case provider.EvRateLimited: rl = true
		case provider.EvFinal: e := ev; final = &e
		}
	}
	res, err := s.Wait()
	if err != nil { t.Fatal(err) }
	if len(perms) != 2 || perms[0].Decision != "deny" || perms[1].Decision != "allow" { t.Fatalf("perms %+v", perms) }
	if !rl { t.Fatal("rate limit event not surfaced") }
	if final == nil || string(final.Final) != `{"greeting":"hi","n":7}` { t.Fatalf("final %+v", final) }
	if res.Handle != "sess-1" || res.Usage.Turns != 3 || res.Usage.CostUSD != 0.07 { t.Fatalf("result %+v", res) }
}

func TestArgsAndEnv(t *testing.T) {
	spec := provider.SessionSpec{Model: "m1", Budget: provider.Budget{MaxTurns: 9}, Resume: "s9", OutputSchema: []byte(`{}`),
		Env: []string{"A=1", "ANTHROPIC_API_KEY=k", "B=2"}}
	got := args(spec)
	for _, want := range []string{"-p", "--permission-prompt-tool", "stdio", "--model", "m1", "--max-turns", "9", "--resume", "s9", "--json-schema", "--disallowedTools"} {
		if !contains(got, want) { t.Fatalf("missing %q in %v", want, got) }
	}
	if contains(got, "--bare") { t.Fatal("--bare must never be passed") }
	env := childEnv(spec)
	for _, e := range env { if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") { t.Fatal("api key leaked") } }
	spec.Env = append(spec.Env, "SIRDAR_BILLING=api")
	if !containsPrefix(childEnv(spec), "ANTHROPIC_API_KEY=") { t.Fatal("api billing must keep the key") }
}

func TestFakeBinaryStdinDenyText(t *testing.T) { /* assert the control_response written to the fake's stdin carried behavior deny + message; the fake echoes stdin lines to stderr prefixed "STDIN:" so the test can read them from Result.ExitErr or a captured stderr writer */ }
```

- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** per the behaviour list. **Step 4: Run** → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add Claude Code provider over stream-json with policy-driven permissions"`

---

### Task 9: Codex provider (app-server JSON-RPC)

**Files:**
- Create: `internal/provider/codex/codex.go`, `internal/provider/codex/rpc.go`
- Test: `internal/provider/codex/codex_test.go`, `internal/provider/codex/testdata/script-basic.jsonl`

**Interfaces:**
- Consumes: `provider.*`; wire shapes from `06-wire-formats.md`.
- Produces: `func New() provider.Provider` (Name `"codex"`).

Behaviour:
- Spawn `<binary> app-server` (binary default `codex`), stdio pipes. `rpc.go`: `type conn` with `call(method, params) (json.RawMessage, error)` (increments id, writes, waits on a per-id channel), `notify(method, params)`, `reply(id, result)`, and a reader goroutine that routes responses by id, notifications to `onNotify(method, params)`, and server requests (have both `id` and `method`) to `onRequest(id, method, params)`.
- Session start: `initialize {"clientInfo":{"name":"sirdar","title":"Sirdar","version":<version>}}` → `initialized` (notification) → if `spec.Resume != ""` then `thread/resume {"threadId":Resume, "sandbox":"read-only","approvalPolicy":"never"}` else `thread/start {"cwd":Cwd,"sandbox":"read-only","approvalPolicy":"never", "model":Model (omit if empty)}`; record `result.thread.id` as handle → `turn/start {"threadId":id,"input":[{"type":"text","text":Prompt}, {"type":"localImage","path":p}...],"outputSchema":<schema object>}`.
- Notification mapping: `item/started` with `item.type == commandExecution|mcpToolCall|fileChange|webSearch` → `EvToolStarted` (Tool = item.type or `mcpToolCall` server/tool names, Input = item); `item/completed` for those → `EvToolFinished`; `item/completed` with `agentMessage` and `phase == "final_answer"` → remember text; `phase == "commentary"` → `EvAssistantText`; `agentMessage` with non-null `questions` → `EvQuestion`; `thread/tokenUsage/updated` → `EvUsage` (tokens from `tokenUsage.total`; CostUSD 0); `account/rateLimits/updated` → `EvRateLimited` only when `primary.usedPercent >= 100` (else `EvSystem`) with `ResetsAt` from `primary.resetsAt`; `turn/completed` → `EvFinal` with `Final` = final_answer text parsed as JSON if it parses, else `Text`; if `turn.status == "failed"` → `EvError{Text: turn.error.message}` then close.
- Server requests: `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval` → reply `{"decision":"decline"}` and emit `EvPermission{Decision:"deny"}`; `item/tool/requestUserInput` → reply `{"answers":{}}` and emit `EvQuestion`; `mcpServer/elicitation/request` → reply `{"action":"decline"}`.
- `Send(userText)` → another `turn/start` on the same thread with the text (and the same `outputSchema`).
- `Wait()`: after final, send `thread/unsubscribe` best effort, close stdin, wait for exit (kill after 5s). `Cancel()`: `turn/interrupt {"threadId","turnId"}` then close stdin.
- `Doctor(binary)`: `<binary> --version`; `<binary> login status` (OK if output contains "Logged in"); spawn app-server, `initialize`, `mcpServerStatus/list` → detail lists server names; shut down.

**Fake server for tests.** Same re-exec pattern with `SIRDAR_FAKE_CODEX=<script>`. Script directives: a plain JSON line is written to stdout; `{"$expect":"initialize"}` blocks until a stdin line whose `method` matches, and remembers its `id`; `{"$reply":{...}}` writes a response using the last expected id; `{"$expect_request":"item/commandExecution/requestApproval","id":77}` writes a server request with that id then blocks until a stdin line with `"id":77` arrives, storing its `result`. `testdata/script-basic.jsonl` mirrors the verified capture: initialize → reply; expect `thread/start` → reply with `thread.id: "th-1"`; expect `turn/start` → reply turn inProgress; `turn/started`; `item/started` commandExecution; server request `item/commandExecution/requestApproval` (assert decline); `item/completed` commandExecution; `item/completed` agentMessage commentary; `thread/tokenUsage/updated`; `account/rateLimits/updated` usedPercent 91; `item/completed` agentMessage final_answer with text `{"greeting":"hi","n":7}`; `turn/completed` status completed.

- [ ] **Step 1: Write the failing tests**: `TestBasicSession` (asserts one `EvPermission` deny, `EvUsage` tokens 17270 total input… use the fixture numbers, `EvFinal.Final == {"greeting":"hi","n":7}`, handle `th-1`, `EvRateLimited` NOT emitted at 91%); `TestThreadStartParams` (captured `thread/start` params contain `"sandbox":"read-only"` and `"approvalPolicy":"never"`; `turn/start` includes `outputSchema` and a `localImage` item when `spec.Images` set); `TestTurnFailed` (script ends with `turn/completed` status failed → `EvError` and `Wait` returns an error).

- [ ] **Step 2–4:** FAIL → implement → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add Codex provider over app-server JSON-RPC"`

---

### Task 10: Prompt assembly, playbooks, and embedded schemas

**Files:**
- Create: `internal/prompt/prompt.go`, `internal/prompt/playbooks.go`, `internal/prompt/preamble.md`, `internal/prompt/schemas/triage.json`, `internal/prompt/schemas/rca.json`, `internal/prompt/playbooks/{10-helpdesk,20-logs,30-apm,40-database,50-code}.md` (scaffold sources, embedded)
- Test: `internal/prompt/prompt_test.go`, `internal/prompt/testdata/{triage.golden.md,rca.golden.md}`

**Interfaces:**
- Consumes: `ticket.Bundle`.
- Produces:

```go
package prompt

type Playbook struct{ Name, Body string }
func LoadPlaybooks(dir string) ([]Playbook, error) // *.md sorted by filename; missing dir => empty, nil
func ScaffoldPlaybooks(dir string) error            // writes the five embedded defaults; never overwrites

//go:embed schemas/triage.json
var TriageSchema []byte
//go:embed schemas/rca.json
var RCASchema []byte // top-level {"rca":{...},"resolution":{...}}

type TriageInput struct {
	Bundle    ticket.Bundle
	BundleDir string      // absolute
	Playbooks []Playbook
	ThreadHead string     // first 40 lines of thread.md
}
type RCAInput struct {
	TriageInput
	TriageNote string     // full markdown of the triage note
	Resolution string     // human text
	PRTitle, PRBody, PRDiff string // may be empty
	PRURL string
}
func Triage(in TriageInput) string
func RCA(in RCAInput) string
```

`preamble.md` content (embedded; copy verbatim into the file):

```markdown
You are Sirdar, an L2 support engineer's investigation agent. You are running inside the
workspace codebase with read-only access and with the evidence tools the workspace has
configured (MCP servers). Your job is to produce a note a human will review, not to fix anything.

Rules:
1. This run is read-only. Do not edit files, do not run commands that change state. If a tool
   is denied, do not retry it; note what you wanted and why.
2. Every claim in the note cites its source: a log query and result, an APM query and result,
   a database query and row count, a file:line, or a screenshot in the bundle.
3. An absence is not a finding. Before writing "no errors were logged" or "the endpoint was not
   called", run a control query proving the same source captures that event type in that
   window, and cite both.
4. Timestamps state their timezone. Say which timezone a source stores.
5. Never guess a ticket, customer or record match. If the identifier in the ticket does not
   resolve unambiguously, say so under open questions.
6. When information is missing, stop and report it under open questions rather than inventing.
7. Translate faithfully. Preserve tone and urgency; quote the original wording where the exact
   phrase matters.
8. Answer only with the JSON object the schema describes. No prose before or after it.
```

Schemas: write full JSON Schema draft-07 documents matching the spec's abridged shapes, with `additionalProperties: false`, required fields for every top-level key, enums for `confidence`, `classification`, `severity`, `verdict`, `resolutionType`, `type` of prevention, `scope`. Resolution `codeChange` and `dataChange` are `oneOf [object, null]`; every string inside them is `["string","null"]`.

`Triage(in)` layout: preamble; `# Playbooks` then each as `## <Name>` + body; `# Ticket` with key, title, priority, tracker URL, helpdesk URL, customer + id, `Bundle directory: <abs>` and the file list; `## Conversation (first lines)` fenced ThreadHead; `## Warnings` bullets if any; `# Output` with per-field guidance lines (one sentence each, from the spec) and the schema in a fenced json block; closing line "Respond with the JSON object only." `RCA(in)` adds `# Triage note` (fenced), `# Resolution as reported by the engineer` (fenced), `# Merged pull request` (title, body, and diff fenced, each only if non-empty) and the audit rule sentence, and uses the RCA schema and guidance.

- [ ] **Step 1: Write the failing tests**: golden tests for `Triage` and `RCA` with a fixed bundle and two tiny playbooks; `LoadPlaybooks` ordering by filename and empty-dir behaviour; `ScaffoldPlaybooks` writes five files and does not overwrite an existing one; both schemas parse as JSON and compile with `jsonschema` (Task 11's lib; here just `json.Valid`).

- [ ] **Step 2–4:** FAIL → implement → PASS (`UPDATE_GOLDEN=1` to write goldens the first time, then inspect them by eye).

- [ ] **Step 5: Commit** — `git commit -m "Add prompt assembly, playbook scaffold, and note schemas"`

---

### Task 11: Note validation, templates, rendering, frontmatter update, digest

**Files:**
- Create: `internal/note/validate.go`, `internal/note/render.go`, `internal/note/frontmatter.go`, `internal/note/digest.go`, `internal/note/templates/{triage,rca,resolution}.md.tmpl`
- Test: `internal/note/note_test.go`, `internal/note/testdata/*.golden.md`

**Interfaces:**
- Consumes: `prompt.TriageSchema`, `prompt.RCASchema`, `store.RegisterRow`.
- Produces:

```go
package note

type Kind string
const (Triage Kind = "triage"; RCA Kind = "rca"; Resolution Kind = "resolution")

// Validate returns a joined, human-readable list of schema violations (path: problem).
func Validate(kind Kind, doc []byte) error // kind Triage uses TriageSchema; RCA validates the combined rca+resolution doc

type Meta struct {
	Key, TrackerURL, HelpdeskID, HelpdeskURL, Customer, CustomerID string
	Date, DateReported string // YYYY-MM-DD
	Priority, Service string
	RunID, Provider string
	Links struct{ Triage, RCA, Resolution string } // wiki-link targets (file stems)
}

type Renderer struct{ TemplatesDir string } // "" = embedded defaults
func (r Renderer) Render(kind Kind, doc []byte, m Meta) (string, error)
// doc for RCA and Resolution is the combined JSON; the template reads .rca or .resolution.
func (r Renderer) Check() error // parses templates and renders a built-in sample; used by doctor and init

func Filename(pattern, key, slug string) string // "{key} {slug}.md" -> "OMNI-1 export-fails.md"
func Slug(title string) string                   // lowercase, ascii letters/digits/hyphen, max 60

// frontmatter.go
func UpdateTriageStatus(path, status string, links map[string]string) error // rewrites only the frontmatter keys status, rca, resolution

// digest.go
type DigestRow struct{ Key, Priority, Issue, Confidence, Classification, State, RunID, Reason string }
func Digest(rows []DigestRow) string // aligned text table + reasons block for blocked/failed
```

Templates: Go `text/template` with funcs `join`, `fill` (renders `<fill: NAME>` when the value is nil/empty), `date`. Default templates reproduce the spec's frontmatter keys and the vault section order; `resolution.md.tmpl` emits `status: proposed` and `<fill: ...>` markers for null audit fields.

- [ ] **Step 1: Write the failing tests**: `Validate` accepts a full valid triage doc and rejects one missing `rootCause.confidence` with a message containing that path; RCA doc with `triageReview.verdict: "maybe"` rejected; golden renders for all three kinds with the embedded templates; override test with a custom `triage.md.tmpl` in a temp dir; `Check()` fails on a template with a syntax error; `Filename`/`Slug`; `UpdateTriageStatus` changes only the three keys and leaves the body byte-identical; `Digest` golden.

- [ ] **Step 2–4:** FAIL → implement → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add note validation, templates, rendering, and digest"`

---

### Task 12: Run lifecycle (prepare, execute, complete, resume, pool)

**Files:**
- Create: `internal/run/run.go`, `internal/run/prepare.go`, `internal/run/execute.go`, `internal/run/pool.go`
- Test: `internal/run/run_test.go` (stub provider + stub sources; no real binaries)

**Interfaces:**
- Consumes: everything above.
- Produces:

```go
package run

type Deps struct {
	Config   *config.Config
	Tracker  source.Tracker   // may be nil
	Helpdesk source.Helpdesk  // may be nil
	Provider provider.Provider
	Creds    config.Resolver
	Now      func() time.Time
	Stderr   io.Writer        // progress lines
	Stdin    io.Reader        // for resume answers
	Env      []string         // base child env (os.Environ())
}

type Options struct {
	Model       string
	Concurrency int
	DryRun      bool
}

type RCAOptions struct {
	Options
	PRURL      string
	Resolution string
}

type Outcome struct {
	Key    string
	State  store.State
	Digest note.DigestRow
}

type Runner struct{ Deps }
func (r *Runner) Triage(ctx context.Context, keys []string, o Options) ([]Outcome, error)
func (r *Runner) RCA(ctx context.Context, key string, o RCAOptions) (Outcome, error)
func (r *Runner) Resume(ctx context.Context, runID string) (Outcome, error)
func ExitCode(outs []Outcome) int // 1 if any failed/over_budget else 0
```

Prepare (`prepare.go`): `store.Create` → state `preparing` → tracker `Get(key)` (if configured) → helpdesk id = `Tracker.HelpdeskRef` or key → helpdesk `Get`, `Threads`, `Attachments(id, bundleDir/attachments)` (attachment error → `Warnings`, continue) → `ticket.WriteBundle` → build prompt (`prompt.Triage` or `prompt.RCA`) → write `prompt.md`. For rca: `store.LatestNote(root, key, triage)` must exist else error `no triage note for KEY; run triage first`; `--pr` → if `gh` on PATH, `gh pr view <url> --json title,body,mergedAt` and `gh pr diff <url>` → `bundle/pr.md`, `bundle/pr.diff`, else warning; `--resolution` → `bundle/resolution.md`. Any error before the agent starts → state `failed` with `Reason`.

Execute (`execute.go`): build `SessionSpec` (Cwd = config.Root, Policy from config, Budget, Model, Images = image attachments (Codex only; Claude reads files), Env = Deps.Env + `SIRDAR_BILLING=<billing>`, Binary from config.Providers). Start session; state `running`; loop over `Events()`: append every event to `events.jsonl`; print one progress line per tool/permission/final/error to Stderr; on `EvUsage` update state usage and check `MaxUSD`/`MaxTurns` → `Cancel()` and `over_budget`; a wall-clock timer of `MaxMinutes` → `Cancel()` and `over_budget`; `EvQuestion` → after the session ends, state `blocked` with `Reason: "agent asked: …"`; `EvRateLimited` → `blocked` with reason and `ResetsAt`; `EvError` malformed counter: ten consecutive → `Cancel()`, `failed`. On `EvFinal`: if `Final == nil` treat `Text` as candidate JSON; `note.Validate`; on failure and `retries == 0` → `Send("Your previous answer did not match the schema: <errors>. Reply again with the corrected JSON object only.")`, wait for the next `EvFinal`; on second failure → write `result.raw.txt`, `failed`. On success → write `result.json`, render note(s) via `note.Renderer{TemplatesDir}` with `Meta` from the bundle, write `note.md` (+ `note-resolution.md` and `playbook-suggestions.md` for rca), copy into `notes.dir` using `note.Filename`, update triage note status for rca, append register rows, state `completed`. Ctrl-C (`ctx.Done()`) → `Cancel()` all, states `blocked` with handles.

Resume (`run.go`): `store.Open(runID)`; if `Reason` starts with `agent asked:` print the question to Stderr, read one line from Stdin, use it as the follow-up text; otherwise use `"Continue where you left off and produce the JSON note."`; start a session with `Resume = state.Handle` and `Prompt` = that text; then the same execute loop.

Pool (`pool.go`): `concurrency` workers over keys; shared `pause` channel: when any run sees `EvRateLimited` with `ResetsAt` in the future, all workers wait until then (or ctx done) before starting new runs; running ones continue.

- [ ] **Step 1: Write the failing tests** with a `stubProvider` whose `Start` returns a scripted `[]provider.Event` and records `SessionSpec` and `Send` calls, and stub sources returning the Task 3 sample bundle:
  - `TestTriageHappyPath`: events = tool started/finished, usage, final valid JSON → state `completed`, `note.md` exists in run dir and in notes dir with the configured filename, `register.jsonl` has one row, `events.jsonl` has as many lines as events, `prompt.md` contains the playbook text, `SessionSpec.Env` contains `SIRDAR_BILLING=subscription` and `Policy.BashAllow` equals config.
  - `TestSchemaRetryThenFail`: two invalid finals → `Send` called once with text containing "did not match the schema", state `failed`, `result.raw.txt` exists, no note written.
  - `TestOverBudgetUSD`: usage event with CostUSD 6 and MaxUSD 5 → `Cancel` called, state `over_budget`.
  - `TestBlockedOnQuestion`: `EvQuestion` then final missing → state `blocked`, `Handle` persisted; `Resume` with Stdin "answer" starts a session with `Resume == handle` and `Prompt == "answer"`.
  - `TestPrepareFailsFast`: helpdesk `Get` returns `source.Error{Auth}` → state `failed`, provider never started.
  - `TestAttachmentWarning`: `Attachments` error → run proceeds, `Warnings` in prompt.
  - `TestRCARequiresTriageNote`: no triage note → error mentions `run triage first`; with a note → prompt contains it, two notes written, triage note frontmatter updated, two register rows.
  - `TestDryRun`: bundle + prompt written, provider not started, state `completed` with `Reason: "dry-run"`.
  - `TestPoolRateLimitPause`: two keys, concurrency 2, first run emits `EvRateLimited` with `ResetsAt` = now+50ms → second key's session starts after that instant.

- [ ] **Step 2–4:** FAIL → implement → PASS.

- [ ] **Step 5: Commit** — `git commit -m "Add run lifecycle: prepare, execute, complete, resume, pool"`

---

### Task 13: CLI commands (init, doctor, triage, rca, resume, runs, register)

**Files:**
- Create: `cmd/sirdar/cmd_init.go`, `cmd_doctor.go`, `cmd_triage.go`, `cmd_rca.go`, `cmd_resume.go`, `cmd_runs.go`, `cmd_register.go`, `cmd/sirdar/wire.go` (builds `run.Deps` from config: sources from `config.Sources` via `plugin.Start` or `zohodesk.New` with resolved token; provider by name; `MacKeychain` on darwin)
- Modify: `cmd/sirdar/main.go` (register commands in `init()`)
- Test: `cmd/sirdar/cli_test.go` (end to end with the file adapter and the fake Claude binary from Task 8 via `providers.claude.path`)

Flags: `triage KEY... [--provider] [--model] [--concurrency N] [--dry-run]`; `rca KEY [--pr URL] [--resolution TEXT|@file] [--provider] [--model]`; `resume RUN_ID`; `runs [KEY] [--json]`; `register [--markdown]`; `init [--templates] [--force]`; `doctor`.

`init`: create `.sirdar/`, write `config.yaml` from `config.DefaultConfigYAML` (workspace name = base of cwd), `prompt.ScaffoldPlaybooks`, with `--templates` write the embedded note templates into `.sirdar/templates/`, append `.sirdar/runs/` and `.sirdar/register.jsonl` to `.git/info/exclude` when `.git` exists; refuse to overwrite `config.yaml` unless `--force`.

`doctor`: config loads; provider `Doctor` checks; each configured source: start and `Describe` (plugin) or `GET /api/v1/organizations` style ping (zohodesk: `Get` on a bogus id must return `NotFound`, not `Auth`); notes dir writable (create a temp file); templates `Check()`; print a table `[OK]/[!!] name — detail`; exit 1 if any failed.

`triage`: parse, build deps, `Runner.Triage`, print `note.Digest`, exit `run.ExitCode`. `rca`: likewise, print the two note paths. `runs`: `store.List` table (run id, key, kind, state, updated, reason). `register`: `store.ReadRegister` grouped per key; `--markdown` prints `| # | Issue | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |` rows.

**Fake Claude for the CLI test**: `cmd/sirdar/testdata/fakeclaude.sh` — a POSIX shell script that ignores stdin, prints an `init` line and a `result` line whose `structured_output` is a complete, valid triage document (`testdata/triage-doc.json`), then exits 0. The test writes a temp workspace: `.sirdar/config.yaml` pointing `sources.helpdesk`/`tracker` at the file adapter (`adapter: exec`, `command: <built file adapter> -file <fixture>`), `providers.claude.path: <fakeclaude.sh>`, `notes.dir: <tmp>/notes`; then runs `run([]string{"triage","OMNI-1"})` and asserts exit 0, digest printed, note file exists; then `run([]string{"rca","OMNI-1","--resolution","fixed by PR"})` with a second fake script that returns a valid rca+resolution doc (`testdata/rca-doc.json`) and asserts both notes exist and the register has three rows. `runs` and `register` print without error. `triage --dry-run` on a fresh key leaves `prompt.md` and no note.

- [ ] **Step 1: Write the failing tests** as described (build the file adapter once in `TestMain`).
- [ ] **Step 2–4:** FAIL → implement → PASS. Also `make build && ./sirdar doctor` in a scratch workspace created by `./sirdar init` must print the table and exit 1 (no sources configured) without panicking.
- [ ] **Step 5: Commit** — `git commit -m "Add CLI commands and end-to-end test with the file adapter"`

---

### Task 14: Docs and release hygiene

**Files:**
- Modify: `README.md` (install, quick start: `init` → edit config → `doctor` → `triage`, config reference table, note types, adapters link, licensing note about running the user's own CLI login)
- Create: `docs/config.md` (every key with default), `LICENSE` (Apache-2.0, copyright the author), `.goreleaser.yaml` (darwin/linux amd64+arm64 archives; no publishing step yet)
- Test: `go vet ./... && go test ./...` clean; `make build` produces `./sirdar`.

- [ ] **Step 1: Write docs.** **Step 2: Run** `go vet ./... && go test ./...` → PASS. **Step 3: Commit** — `git commit -m "Add README, config reference, license, and release config"`

---

## Golden set (manual, after Task 13)

Not a code task. With the real config in the OXO.APIs workspace (`.sirdar/` excluded from git), run `sirdar triage --dry-run OMNI-XXXX` for one of the seven 2026-09-10 tickets, inspect `prompt.md`, then a real `sirdar triage` on one ticket with Claude, compare the note against the human-written one, and record findings in `docs/research/07-golden-set-notes.md`. Copy the seven bundles to `~/.sirdar/golden/<KEY>/`.
