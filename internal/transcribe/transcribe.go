// Package transcribe turns an audio attachment into text by running the
// operator's own transcription command over it.
//
// Nothing here knows how to transcribe anything. The workspace names a
// command — whisper.cpp, OpenAI's whisper CLI, a wrapper script — and this
// package runs it under the constraints a triage run needs: no shell, a
// fixed argv built from the template, stdin closed, an environment cut down
// to PATH, HOME and LANG, a per-file timeout and a budget for the batch.
// Where the audio goes is therefore entirely the operator's choice: a local
// model never leaves the machine, a command that posts to an API sends the
// customer's voice note to that API.
package transcribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
)

const (
	// PerFileTimeout caps one transcription. A voice note is seconds of
	// audio; a command still running after two minutes is stuck on
	// something other than this file.
	PerFileTimeout = 2 * time.Minute

	// TotalTimeout caps the whole batch, so a ticket carrying thirty
	// attachments cannot hold a run for an hour before the session starts.
	TotalTimeout = 10 * time.Minute

	// probeTimeout caps the duration probe, which is a metadata read.
	probeTimeout = 15 * time.Second

	// maxCapture is how much of a command's output is held in memory. A
	// transcript is text; anything past this is a command writing
	// something else at us.
	maxCapture = 4 << 20

	// killGrace is how long a timed-out command's process group has to
	// die before exec gives up waiting on the pipes it holds — the same
	// backstop agenttools/exec.go uses for its own child processes.
	killGrace = 2 * time.Second

	// probeCapture bounds ffprobe's own output: it is asked for one
	// number, so anything past a few hundred bytes is not a duration.
	probeCapture = 4 << 10
)

// Options configures a Transcriber. Command is required; the rest carry
// the caller's defaults, and a zero value turns its check off.
type Options struct {
	// Command is the template, e.g.
	// "whisper-cli -m /models/ggml-large.bin -l auto -otxt -of {out} {in}".
	// It is split into argv once and never handed to a shell.
	Command string

	// MaxSeconds is the longest audio that will be transcribed; 0 means
	// no limit. The duration is read with ffprobe, so with no ffprobe on
	// PATH the check cannot be made and the file is transcribed anyway,
	// bounded by PerFileTimeout.
	MaxSeconds int

	// MaxFiles caps how many attachments one bundle transcribes; 0 means
	// no limit.
	MaxFiles int

	// Formats are the file extensions to treat as audio, without the dot
	// and in lower case.
	Formats []string

	// Env is the environment to filter down for the child process;
	// os.Environ() when nil.
	Env []string

	// Now is the clock the batch budget is measured against; time.Now
	// when nil.
	Now func() time.Time
}

// Result is one finished transcription.
type Result struct {
	Text string // the transcript, with no header line
	Tool string // the command's own name, e.g. "whisper-cli"
	// Language is the language code or name the tool reported, or the one
	// the command pinned with -l/--language. Empty when neither said.
	Language string
}

// Transcriber runs one workspace's transcription command over the audio in
// one bundle. It is not safe for concurrent use: the file count and the
// batch budget are per bundle, and a bundle is assembled by one goroutine.
type Transcriber struct {
	argv     []string
	opts     Options
	formats  map[string]bool
	tool     string
	pinned   string // language from -l/--language, "" when auto or absent
	env      []string
	deadline time.Time
	count    int

	// perFile is PerFileTimeout, overridable inside this package so a
	// test can assert the timeout without waiting two minutes for it.
	perFile time.Duration
}

// ErrOverCap is returned once the batch has transcribed MaxFiles
// attachments or spent TotalTimeout on them. The attachment is untouched;
// the caller reports it the way it reports any attachment it could not
// read.
var ErrOverCap = errors.New("transcribe: the run's transcription cap is reached")

// New builds a Transcriber from the workspace's options. It fails only on
// a command that cannot be turned into an argv, which config validation
// has already refused, so a workspace that loaded gets a working one.
func New(o Options) (*Transcriber, error) {
	argv, err := SplitCommand(o.Command)
	if err != nil {
		return nil, err
	}
	if !hasPlaceholder(argv, "{in}") {
		return nil, fmt.Errorf("transcribe: the command names no {in}, so it would never be given the audio file")
	}
	formats := make(map[string]bool, len(o.Formats))
	for _, f := range o.Formats {
		if f = normalExt(f); f != "" {
			formats[f] = true
		}
	}
	t := &Transcriber{
		argv:    argv,
		opts:    o,
		formats: formats,
		tool:    filepath.Base(argv[0]),
		pinned:  pinnedLanguage(argv),
		env:     childEnv(o.Env),
		perFile: PerFileTimeout,
	}
	return t, nil
}

// Tool is the name of the command this Transcriber runs, which is what a
// transcript's header line credits.
func (t *Transcriber) Tool() string { return t.tool }

// Handles reports whether an attachment is one this command is configured
// to transcribe. The filename extension decides, falling back to the media
// type's subtype when the name carries no extension — a helpdesk that
// serves a voice note as "audio/ogg" with an opaque id for a name is
// common enough to be worth reading.
func (t *Transcriber) Handles(name, mimeType string) bool {
	ext := normalExt(filepath.Ext(name))
	if ext == "" {
		ext = subtypeExt(mimeType)
	}
	return ext != "" && t.formats[ext]
}

// Run transcribes the audio at path. Every failure is the caller's to
// report as a warning: nothing here is a reason to fail a run.
func (t *Transcriber) Run(ctx context.Context, path string) (Result, error) {
	if err := t.charge(); err != nil {
		return Result{}, err
	}
	if secs, ok := t.duration(ctx, path); ok && t.opts.MaxSeconds > 0 && secs > float64(t.opts.MaxSeconds) {
		return Result{}, fmt.Errorf("the audio is %ds long, over the %ds attachments.transcribe.maxSeconds limit",
			int(secs+0.5), t.opts.MaxSeconds)
	}

	work, err := os.MkdirTemp("", "sirdar-transcribe-")
	if err != nil {
		return Result{}, fmt.Errorf("transcription workspace: %w", err)
	}
	defer os.RemoveAll(work)

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if !validStem(stem) {
		stem = "audio"
	}
	out := filepath.Join(work, stem)

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	argv := substitute(t.argv, map[string]string{"{in}": abs, "{out}": out, "{outdir}": work})

	stdout, stderr, combined, err := t.exec(ctx, argv, work)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w%s", t.tool, err, tail(combined))
	}

	text := collect(work, stem, stdout)
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("%s produced no transcript%s", t.tool, tail(combined))
	}
	return Result{
		Text:     strings.TrimRight(text, "\n"),
		Tool:     t.tool,
		Language: t.language(stderr),
	}, nil
}

// validStem reports whether stem is safe to join under the transcription
// workspace and hand to the command as {out}: no path separator — either
// OS's, since a filename can carry a separator this build's OS does not
// use as one — and not a "." or ".." that would resolve to the workspace
// itself or its parent. An attachment's filename is customer-controlled,
// and {out} becomes a path the command is told to write to.
func validStem(stem string) bool {
	if stem == "" || stem == "." || stem == ".." {
		return false
	}
	return !strings.ContainsAny(stem, `/\`)
}

// charge books this file against the batch's two caps, starting the clock
// on the first call.
func (t *Transcriber) charge() error {
	now := t.now()
	if t.deadline.IsZero() {
		t.deadline = now.Add(TotalTimeout)
	}
	if t.opts.MaxFiles > 0 && t.count >= t.opts.MaxFiles {
		return fmt.Errorf("%w: attachments.transcribe.maxFiles is %d", ErrOverCap, t.opts.MaxFiles)
	}
	if !now.Before(t.deadline) {
		return fmt.Errorf("%w: the %s budget for this bundle is spent", ErrOverCap, TotalTimeout)
	}
	t.count++
	return nil
}

func (t *Transcriber) now() time.Time {
	if t.opts.Now != nil {
		return t.opts.Now()
	}
	return time.Now()
}

// exec runs one command with everything a triage run wants held down:
// no shell, stdin closed, a cut-down environment, a per-file timeout that
// never outlives the batch budget, a cap on how much output is kept, and
// its own process group so the timeout can kill whatever it spawned. A
// wrapper script that traps or ignores SIGTERM and never exits would
// otherwise leave cmd.Run blocked past the timeout with the child still
// running — see agenttools/exec.go, which this mirrors.
func (t *Transcriber) exec(ctx context.Context, argv []string, dir string) (stdout, stderr, combined string, err error) {
	limit := t.perFile
	if left := t.deadline.Sub(t.now()); !t.deadline.IsZero() && left < limit {
		limit = left
	}
	if limit <= 0 {
		return "", "", "", fmt.Errorf("%w: the %s budget for this bundle is spent", ErrOverCap, TotalTimeout)
	}
	cctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", "", "", fmt.Errorf("%s is not on PATH", argv[0])
	}

	var outBuf, errBuf, bothBuf capped
	outBuf.limit, errBuf.limit, bothBuf.limit = maxCapture, maxCapture, maxCapture

	cmd := exec.CommandContext(cctx, bin, argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = nil // an empty pipe: the command is never asked a question
	cmd.Env = t.env
	cmd.Stdout = io.MultiWriter(&outBuf, &bothBuf)
	cmd.Stderr = io.MultiWriter(&errBuf, &bothBuf)
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = killGrace

	runErr := cmd.Run()
	if cctx.Err() != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return "", "", bothBuf.String(), fmt.Errorf("timed out after %s", limit.Round(time.Second))
	}
	if runErr != nil {
		return "", "", bothBuf.String(), runErr
	}
	return outBuf.String(), errBuf.String(), bothBuf.String(), nil
}

// duration asks ffprobe how long the audio is. Without ffprobe there is no
// answer, which is reported as "unknown" rather than guessed: the caller
// then transcribes the file and lets PerFileTimeout bound it.
func (t *Transcriber) duration(ctx context.Context, path string) (float64, bool) {
	if t.opts.MaxSeconds <= 0 {
		return 0, false
	}
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// -i names the input explicitly rather than trailing it as a bare
	// argument: a filename beginning with "-" would otherwise be read as
	// another flag.
	cmd := exec.CommandContext(cctx, bin,
		"-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", "-i", path)
	cmd.Stdin = nil
	cmd.Env = t.env
	var out capped
	out.limit = probeCapture
	cmd.Stdout = &out
	cmd.Stderr = nil
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = killGrace

	if err := cmd.Run(); err != nil {
		return 0, false
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil || secs <= 0 {
		return 0, false
	}
	return secs, true
}

// language is what to credit the transcript to: the code the command
// pinned, else whatever the tool said it detected on stderr.
func (t *Transcriber) language(stderr string) string {
	if t.pinned != "" {
		return t.pinned
	}
	return detectedLanguage(stderr)
}

// Header is the first line of a transcript file: what produced it, from
// what, and in what language when that is known. A transcript in a bundle
// is evidence with a provenance, and the agent quoting it should be able
// to say where it came from.
func Header(name string, r Result) string {
	line := "# transcript of " + name
	if r.Tool != "" {
		line += " — " + r.Tool
	}
	if r.Language != "" {
		line += " (language: " + r.Language + ")"
	}
	return line
}

// --- command template -------------------------------------------------

// shellOperators are the tokens that would mean something to a shell and
// mean nothing here: the command is executed directly, so a pipe or a
// redirect would be passed to the tool as a literal argument. Refusing
// them is how an operator finds that out at config load rather than from a
// transcript that never appears.
var shellOperators = map[string]bool{
	"|": true, "||": true, "&": true, "&&": true, ";": true, ";;": true,
	">": true, ">>": true, "<": true, "<<": true, "2>": true, "2>&1": true,
}

// SplitCommand splits a command template into argv the way a shell would
// split a simple command, and no further: single quotes are literal,
// double quotes allow a backslash escape, and a backslash outside quotes
// escapes the next character. Nothing is expanded — no variables, no
// globs, no substitution — because nothing here runs a shell.
func SplitCommand(s string) ([]string, error) {
	var (
		argv  []string
		cur   strings.Builder
		open  bool // cur holds a token, even if it is empty ("" as an argument)
		quote rune
	)
	flush := func() {
		if open {
			argv = append(argv, cur.String())
			cur.Reset()
			open = false
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			cur.WriteRune(c)
		case quote == '"':
			if c == '\\' && i+1 < len(runes) {
				next := runes[i+1]
				if next == '"' || next == '\\' {
					cur.WriteRune(next)
					i++
					continue
				}
			}
			if c == '"' {
				quote = 0
				continue
			}
			cur.WriteRune(c)
		case c == '\'' || c == '"':
			quote = c
			open = true
		case c == '\\' && i+1 < len(runes):
			cur.WriteRune(runes[i+1])
			open = true
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteRune(c)
			open = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("transcribe: the command ends inside a %c quote", quote)
	}
	flush()

	if len(argv) == 0 {
		return nil, errors.New("transcribe: the command is empty")
	}
	for _, tok := range argv {
		if shellOperators[tok] {
			return nil, fmt.Errorf("transcribe: %q is a shell operator and the command is not run through a shell; put the pipeline in a script and name the script here", tok)
		}
	}
	return argv, nil
}

// Placeholders are the substitutions a command template may use. {in} is
// the audio file, {out} an output path with no extension (whisper.cpp's
// -of), and {outdir} a directory to write into (whisper's --output_dir).
var Placeholders = []string{"{in}", "{out}", "{outdir}"}

// substitute replaces every placeholder inside every token, so both
// "-of {out}" and "--output_dir={outdir}" work and a path with spaces
// stays one argument.
func substitute(argv []string, vals map[string]string) []string {
	out := make([]string, len(argv))
	for i, tok := range argv {
		for k, v := range vals {
			tok = strings.ReplaceAll(tok, k, v)
		}
		out[i] = tok
	}
	return out
}

func hasPlaceholder(argv []string, name string) bool {
	for _, tok := range argv {
		if strings.Contains(tok, name) {
			return true
		}
	}
	return false
}

// pinnedLanguage reads the language the command fixed with -l or
// --language. "auto" is not a language, so it reports nothing and the
// tool's own detection line is used instead.
func pinnedLanguage(argv []string) string {
	val := ""
	for i, tok := range argv {
		switch {
		case tok == "-l" || tok == "--language":
			if i+1 < len(argv) {
				val = argv[i+1]
			}
		case strings.HasPrefix(tok, "--language="):
			val = strings.TrimPrefix(tok, "--language=")
		}
	}
	val = strings.TrimSpace(val)
	if val == "" || strings.EqualFold(val, "auto") || strings.HasPrefix(val, "{") {
		return ""
	}
	return val
}

// --- output ------------------------------------------------------------

// collect finds the transcript the command produced: a text file it wrote
// into the working directory (whisper.cpp's "<out>.txt", whisper's
// "<stem>.txt"), else whatever it printed on stdout.
func collect(work, stem, stdout string) string {
	if text, ok := readTranscriptFile(filepath.Join(work, stem+".txt")); ok {
		return text
	}
	// Anything else the command left behind, preferring a .txt: the
	// naming rules differ per tool and per output format flag, and the
	// working directory holds nothing but this file's output.
	entries, err := os.ReadDir(work)
	if err == nil {
		best := ""
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if best == "" || (filepath.Ext(e.Name()) == ".txt" && filepath.Ext(best) != ".txt") {
				best = e.Name()
			}
		}
		if best != "" {
			if text, ok := readTranscriptFile(filepath.Join(work, best)); ok {
				return text
			}
		}
	}
	return stdout
}

// readTranscriptFile reads a transcript the command wrote to disk, capped
// at maxCapture bytes so a tool that wrote something other than text — or
// simply a great deal of it — cannot be read whole into memory before
// this package notices. ok is false when the file does not exist or holds
// nothing but whitespace.
func readTranscriptFile(path string) (text string, ok bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxCapture+1))
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", false
	}
	if len(data) <= maxCapture {
		return string(data), true
	}
	cut := maxCapture
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return strings.TrimRight(string(data[:cut]), "\n") +
		"\n\n[transcript truncated at 4 MiB; the file the command wrote is longer]\n", true
}

var (
	// whisper.cpp: "auto-detected language: ar (p = 0.98)"
	cppLanguage = regexp.MustCompile(`(?i)auto-detected language:\s*([A-Za-z][A-Za-z_-]*)`)
	// openai-whisper: "Detected language: Arabic"
	pyLanguage = regexp.MustCompile(`(?i)detected language:\s*([A-Za-z][A-Za-z -]*)`)
)

// detectedLanguage reads the language a tool announced on stderr. stdout
// is never scanned here: for a command with no {out} placeholder, stdout
// is the transcript itself — customer speech — and a match inside it
// would let the customer's own words set Result.Language, which lands in
// the transcript header and the prompt's transcripts note unquoted.
func detectedLanguage(stderr string) string {
	if m := cppLanguage.FindStringSubmatch(stderr); len(m) == 2 {
		return languageLabel(m[1])
	}
	if m := pyLanguage.FindStringSubmatch(stderr); len(m) == 2 {
		return languageLabel(m[1])
	}
	return ""
}

// languageLabel is a detected language cut down to what is safe to drop,
// unquoted, into a transcript header and a prompt note: its first
// whitespace-separated token, at most 32 bytes of it, and only letters
// and hyphens. Anything else reports as undetected rather than carrying
// whatever a tool — or a customer's own speech a tool echoed — put on
// that line.
func languageLabel(s string) string {
	s = strings.TrimSpace(s)
	if fields := strings.Fields(s); len(fields) > 0 {
		s = fields[0]
	} else {
		s = ""
	}
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" || !languageLabelPattern.MatchString(s) {
		return ""
	}
	return s
}

var languageLabelPattern = regexp.MustCompile(`^[A-Za-z-]+$`)

// tail is the part of a failed command's output a warning can carry: one
// line, short enough to sit in a run's warning list.
func tail(output string) string {
	out := strings.TrimSpace(output)
	if out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return ": " + last
}

// --- environment -------------------------------------------------------

// keptEnv is everything the child is told about the machine it runs on: a
// PATH to find its model runner, a HOME because plenty of tools cache
// under it, a LANG so its own output is encoded the way it expects, and a
// TMPDIR so a tool that writes scratch files there — rather than into the
// working directory this package already controls — lands somewhere
// writable instead of guessing. Not the operator's credentials, and not
// the API keys a triage run resolved.
var keptEnv = []string{"PATH", "HOME", "LANG", "TMPDIR"}

// childEnv filters base down to keptEnv, falling back to the process
// environment when the caller named none.
func childEnv(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	var out []string
	for _, name := range keptEnv {
		prefix := name + "="
		for _, kv := range base {
			if strings.HasPrefix(kv, prefix) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// --- small helpers -----------------------------------------------------

// normalExt lower-cases an extension and drops its leading dot, so
// ".OGG", "OGG" and "ogg" are one format.
func normalExt(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
}

// subtypeExt reads a usable extension out of a media type: "audio/ogg" is
// "ogg", "audio/x-m4a" is "m4a".
func subtypeExt(mimeType string) string {
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = mimeType[:i]
	}
	i := strings.IndexByte(mimeType, '/')
	if i < 0 {
		return ""
	}
	return normalExt(strings.TrimPrefix(strings.TrimSpace(mimeType[i+1:]), "x-"))
}

// capped is a Writer that keeps the first limit bytes and drops the rest,
// so a command that writes a gigabyte cannot take the run down with it.
// os/exec copies stdout and stderr on separate goroutines, and both land
// in the same combined buffer, so the lock is not optional.
type capped struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
