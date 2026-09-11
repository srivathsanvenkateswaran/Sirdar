package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	// envCodexHome is the variable Codex reads its own directory from:
	// config.toml, auth.json, sessions, caches, skills and plugins.
	envCodexHome = "CODEX_HOME"
	// envKeepHome keeps a session's generated home on disk for debugging.
	envKeepHome = "SIRDAR_KEEP_CODEX_HOME"
	// defaultCodexDir is the home Codex uses when CODEX_HOME is unset.
	defaultCodexDir = ".codex"
	// homePrefix names the generated homes, and is what the stale sweep
	// recognises its own leftovers by.
	homePrefix = "sirdar-codex-home-"
	// staleHomeAge is how old a leftover generated home has to be before
	// Start removes it, for the ordinary case: a home whose session has
	// already reaped it via remove() never reaches the sweep at all, so
	// this only ever prunes what a hard kill left behind. A live session
	// that outlives this is still protected by its lock file (see
	// lockFileName) regardless of how old its directory's mtime gets.
	staleHomeAge = 24 * time.Hour
	// lockFileName holds the pid and start time of the session a
	// generated home belongs to, so the sweep can tell a home whose
	// session is still running from a SIGKILL leftover without trusting
	// mtime alone — a long-running turn is not guaranteed to touch its
	// own home, but its process either is or is not still there.
	lockFileName = ".sirdar-lock"
)

// scratchHome is a generated CODEX_HOME for one session: the user's own
// config.toml with its [mcp_servers] tables replaced by the workspace's,
// their auth.json copied in so the session still bills against their
// login, and every other entry of their real home symlinked so history,
// sessions, skills and caches behave as they always did.
//
// It exists because Codex has no equivalent of Claude Code's
// --strict-mcp-config: MCP servers come from config.toml, and both the
// `-c key=value` overrides and thread/start's `config` object merge into
// that table instead of replacing it (verified against codex-cli 0.154.0;
// see docs/research/06-wire-formats.md). Pointing the session at a home
// whose config.toml names only the workspace's servers is the only
// mechanism that can subtract the operator's own.
type scratchHome struct {
	dir     string
	servers []string
	keep    bool
	once    sync.Once

	// realDir is the operator's own home, and authCopied is the bytes of
	// their auth.json as they were copied in. Together they are what the
	// write-back on Wait compares against: a token Codex refreshed inside
	// the session is worth keeping, but only when the operator's own file
	// has not moved on since.
	realDir    string
	authCopied []byte
	authWarn   []string
}

// newScratchHome writes a home for the stdio servers declared in
// <root>/.mcp.json. A root with no .mcp.json yields a home that declares
// no servers at all, which is what mcp.workspaceOnly asks for in a
// workspace that has written none. The warnings are the loader's own, one
// per skipped entry and one per unset ${VAR}.
//
// env is the session's child environment, not this process's: it is where
// CODEX_HOME is read from, and where a `.mcp.json` ${VAR} is expanded
// from, so a credential internal/run stripped cannot come back through
// the generated config.toml.
func newScratchHome(root string, env []string) (*scratchHome, []string, error) {
	servers, warnings, err := mcpclient.LoadWorkspaceServersEnv(root, env)
	if err != nil {
		return nil, nil, err
	}

	// A SIGKILL leaves a home behind — remove has no chance to run — so
	// each new session clears out yesterday's. Best effort: a sweep that
	// fails is not a reason to lose the run.
	sweepStaleHomes(os.TempDir(), time.Now())

	dir, err := os.MkdirTemp("", homePrefix)
	if err != nil {
		return nil, nil, err
	}
	// MkdirTemp already makes it 0700; say so rather than assume it, since
	// the directory holds a copy of the operator's login.
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	if err := writeLock(dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	h := &scratchHome{dir: dir, keep: envValue(env, envKeepHome) == "1"}
	for _, s := range servers {
		h.servers = append(h.servers, s.Name)
	}

	real := realCodexHome(env)
	h.realDir = real
	if err := h.inherit(real); err != nil {
		h.forceRemove()
		return nil, nil, err
	}
	body := stripMCPServers(readConfig(real)) + renderServers(servers)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		h.forceRemove()
		return nil, nil, err
	}
	return h, warnings, nil
}

// sweepStaleHomes removes generated homes left behind by sessions that
// were killed outright. Only directories this package names, and only
// those a live session does not own: a home whose lock file names a pid
// that is still running is kept whatever its mtime says — mtime alone
// would let a long, quiet turn's home be swept out from under it — and
// everything else falls back to the staleHomeAge check that catches a
// SIGKILL leftover, which has nobody left to hold its lock.
func sweepStaleHomes(tmp string, now time.Time) {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), homePrefix) {
			continue
		}
		dir := filepath.Join(tmp, e.Name())
		if pid, ok := lockPID(dir); ok && processAlive(pid) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < staleHomeAge {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

// writeLock records this session's pid and start time in the generated
// home, so a later sweep — this session's own next run, or another
// session's — can tell it apart from a leftover nobody is holding.
func writeLock(dir string) error {
	body := fmt.Sprintf("%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(filepath.Join(dir, lockFileName), []byte(body), 0o600)
}

// lockPID reads the pid a generated home's lock file names. ok is false
// when the home has no lock file (older than this mechanism, or already
// half torn down) or the file does not parse, in which case the sweep
// falls back to mtime alone rather than treat an unreadable lock as a
// license to keep the directory forever.
func lockPID(dir string) (pid int, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		return 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// processAlive reports whether pid names a running process. Signal 0
// delivers nothing; the kernel call still fails with ESRCH when no process
// by that pid exists, which is enough to tell a live session's lock from a
// leftover one a dead process cannot renew. syscall.Kill is used directly
// rather than os.FindProcess().Signal: on Unix the latter tracks its own
// "already finished" state per Process value and reports that instead of
// asking the kernel, which is wrong for a Process obtained by pid rather
// than from the exec that started it.
func processAlive(pid int) bool {
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// inherit copies the login and links everything else. auth.json is copied
// rather than linked because Codex rewrites it when it refreshes the
// ChatGPT token, and a triage run must not be able to damage the file the
// operator's own `codex` sessions depend on; the copy is the session's to
// refresh. Everything else — sessions, history, state, skills, plugins,
// caches — is symlinked to the real home, so a thread started here is
// still on disk for the resume that a schema retry needs, and so a
// workspace-only run differs from an ordinary one in its MCP servers and
// nothing else.
func (h *scratchHome) inherit(real string) error {
	if real == "" {
		return nil
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		// No home of their own is not an error: Codex creates one.
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if name == "config.toml" {
			continue
		}
		if name == "auth.json" {
			src := filepath.Join(real, name)
			b, err := copyFile(src, filepath.Join(h.dir, name))
			if err != nil {
				return fmt.Errorf("copy %s: %w", src, err)
			}
			h.authCopied = b
			continue
		}
		if err := os.Symlink(filepath.Join(real, name), filepath.Join(h.dir, name)); err != nil {
			return fmt.Errorf("link %s: %w", name, err)
		}
	}
	return nil
}

// apply returns base with CODEX_HOME pointing at the generated home.
func (h *scratchHome) apply(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, e := range base {
		if strings.HasPrefix(e, envCodexHome+"=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, envCodexHome+"="+h.dir)
}

// remove returns the refreshed login to the operator's own home and then
// deletes the generated one, once the session that used it has exited.
// SIRDAR_KEEP_CODEX_HOME=1 keeps the directory for inspection; the
// write-back still happens, because a token left only in a directory
// nobody reads is a token thrown away. A nil home — mcp.workspaceOnly
// off — has nothing to do.
//
// It returns whatever the write-back has to say, so the session can put
// it in the events log rather than swallow it.
func (h *scratchHome) remove() []string {
	if h == nil {
		return nil
	}
	h.once.Do(func() {
		h.authWarn = h.writeBackAuth()
		if h.keep {
			return
		}
		_ = os.RemoveAll(h.dir)
	})
	return h.authWarn
}

// writeBackAuth copies a refreshed auth.json back to the operator's home.
//
// Codex rewrites auth.json when it refreshes the ChatGPT token, and it
// rewrites the session's copy, not theirs. Leaving it there would strand
// the refresh: their own `codex` would go on presenting the token this
// run replaced, and on some refresh flows the old one no longer works.
//
// The write-back happens only when the session's copy differs from what
// was copied in AND the real file is byte-for-byte what it was at the
// copy. Anything else — their own session refreshed it meanwhile, the
// file was replaced by a fresh login — means two writers, and the run
// that did not ask to be an authority on their login stands down and
// says so.
func (h *scratchHome) writeBackAuth() []string {
	if h.realDir == "" || h.authCopied == nil {
		return nil
	}
	dst := filepath.Join(h.realDir, "auth.json")
	now, err := os.ReadFile(filepath.Join(h.dir, "auth.json"))
	if err != nil || bytes.Equal(now, h.authCopied) {
		return nil // nothing was refreshed
	}
	current, err := os.ReadFile(dst)
	if err != nil {
		return []string{"codex auth: the session refreshed its login but " + dst + " could not be read, so it was left alone"}
	}
	if !bytes.Equal(current, h.authCopied) {
		return []string{"codex auth: the session refreshed its login but " + dst +
			" changed underneath it, so the refresh was discarded rather than overwrite yours"}
	}
	if err := writeFileAtomic(dst, now, 0o600); err != nil {
		return []string{"codex auth: writing the refreshed login back to " + dst + " failed: " + err.Error()}
	}
	return []string{"codex auth: the session's token refresh was written back to " + dst}
}

// writeFileAtomic writes through a temporary file in the destination's own
// directory and renames it into place, so a crash mid-write cannot leave
// the operator with half an auth.json and no login at all.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sirdar-auth-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once the rename has succeeded
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// forceRemove discards a half-built home, keep flag or not.
func (h *scratchHome) forceRemove() {
	h.once.Do(func() { _ = os.RemoveAll(h.dir) })
}

// realCodexHome is the home the operator's own codex sessions use.
func realCodexHome(env []string) string {
	if dir := envValue(env, envCodexHome); dir != "" {
		return dir
	}
	if home := envValue(env, "HOME"); home != "" {
		return filepath.Join(home, defaultCodexDir)
	}
	return ""
}

// envValue reads a variable out of a child environment slice.
func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

// readConfig returns the operator's config.toml, or "" when they have none.
func readConfig(real string) string {
	if real == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(real, "config.toml"))
	if err != nil {
		return ""
	}
	return string(b)
}

// mcpServersKey is the root TOML key Codex reads its MCP servers from.
const mcpServersKey = "mcp_servers"

// stripMCPServers removes every mcp_servers declaration from a config.toml
// and leaves the rest — model, reasoning effort, project trust, profiles —
// byte for byte, so the generated home changes which MCP servers the
// session sees and nothing else.
//
// It works on statements rather than lines, because a line is not a unit
// of TOML: a `"""…"""` value can contain the text `[mcp_servers.x]`, and
// a value can span lines inside `[ … ]` or `{ … }`. Reading those as
// headers cost the rest of the file — everything after such a string was
// deleted — so the scan tracks string fences and bracket depth and only
// ever drops a whole statement.
//
// What goes: the `[mcp_servers]` table and its sub-tables (including the
// `[[mcp_servers.x]]` array form), and, at the root table only, a
// `mcp_servers = { … }` assignment or a `mcp_servers.x.y = …` dotted key.
// A `mcp_servers` key under some other table is that table's own key —
// `profiles.dev.mcp_servers` is not Codex's server list — and stays.
//
// Blank lines and comments inside a dropped table go with it; a comment
// written directly above the table that follows is part of that dropped
// run and goes too, which is the one thing here that is not byte-exact.
func stripMCPServers(cfg string) string {
	if cfg == "" {
		return ""
	}
	var b strings.Builder
	dropping, atRoot := false, true
	for _, st := range scanTOML(cfg) {
		switch {
		case st.header:
			atRoot = false
			dropping = st.key == mcpServersKey
		case atRoot && st.key == mcpServersKey:
			continue
		}
		if dropping {
			continue
		}
		b.WriteString(st.text)
	}
	body := strings.TrimRight(b.String(), "\n")
	if body == "" {
		return ""
	}
	return body + "\n"
}

// tomlStatement is one top-level unit of a config.toml: a table header, a
// key/value assignment (however many lines its value spans), a comment, or
// a blank line. text is the source verbatim, newline included, so writing
// the kept statements back out reproduces the original byte for byte.
type tomlStatement struct {
	text   string
	header bool   // a [table] or [[array of tables]] header
	key    string // first segment of the header path or the key path, unquoted
}

// scanTOML splits a config.toml into statements. A newline ends one only
// when it falls outside every string and at bracket depth zero, which is
// what keeps a multi-line string or a multi-line array whole.
//
// This is a lexer, not a parser: it is here to find statement boundaries
// and first keys in a file Codex itself has already accepted, not to
// validate one. Malformed input yields odd statements rather than an
// error, and the worst that costs is a declaration left in place — the
// generated home then has a server too many, which the doctor row and the
// session's own EvSystem event both name.
func scanTOML(cfg string) []tomlStatement {
	var out []tomlStatement
	start, depth := 0, 0
	for i := 0; i < len(cfg); i++ {
		switch cfg[i] {
		case '#':
			for i < len(cfg) && cfg[i] != '\n' {
				i++
			}
			i-- // the newline is this statement's terminator; see below
		case '"', '\'':
			i = skipString(cfg, i)
		case '[', '{':
			depth++
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case '\n':
			if depth == 0 {
				out = append(out, classify(cfg[start:i+1]))
				start = i + 1
			}
		}
	}
	if start < len(cfg) {
		out = append(out, classify(cfg[start:]))
	}
	return out
}

// skipString returns the index of the last byte of the string that opens
// at i, so the caller's loop resumes after it. An unterminated string runs
// to the end of the input, which is what a TOML parser would reject; the
// scan only has to not run away.
func skipString(s string, i int) int {
	quote := s[i]
	fence := strings.Repeat(string(quote), 3)
	if strings.HasPrefix(s[i:], fence) {
		if end := strings.Index(s[i+3:], fence); end >= 0 {
			return i + 3 + end + 2
		}
		return len(s) - 1
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if quote == '"' {
				j++ // \" and \\ are content, not the end
			}
		case quote:
			return j
		case '\n':
			// A single-quoted string may not contain a newline. Treat the
			// line end as the end of it rather than swallowing the file.
			return j - 1
		}
	}
	return len(s) - 1
}

// classify reads a statement's shape off its text.
func classify(text string) tomlStatement {
	st := tomlStatement{text: text}
	trimmed := strings.TrimLeft(text, " \t")
	switch {
	case strings.TrimSpace(trimmed) == "", strings.HasPrefix(trimmed, "#"):
		return st
	case strings.HasPrefix(trimmed, "["):
		st.header = true
		inner := strings.TrimPrefix(strings.TrimPrefix(trimmed, "["), "[")
		st.key = firstKey(inner)
	default:
		st.key = firstKey(trimmed)
	}
	return st
}

// firstKey returns the first segment of a dotted TOML key path, unquoted.
// It is what decides whether a header or an assignment belongs to
// mcp_servers, so the quoted forms matter: `"mcp_servers".x` and
// `'mcp_servers'.x` name the same table as `mcp_servers.x`.
func firstKey(s string) string {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				continue
			}
			if s[i] == '"' {
				break
			}
			b.WriteByte(s[i])
		}
		return b.String()
	case '\'':
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return s[1 : 1+end]
		}
		return s[1:]
	}
	end := strings.IndexAny(s, " \t.=]")
	if end < 0 {
		end = len(s)
	}
	return s[:end]
}

// renderServers writes one [mcp_servers.<name>] table per workspace server.
// LoadWorkspaceServers has already expanded ${VAR} and $VAR in args and env
// values from Sirdar's own environment, so what lands in the TOML is the
// literal the server will be started with.
func renderServers(servers []mcpclient.ServerConfig) string {
	var b strings.Builder
	for _, s := range servers {
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\n", s.Name)
		fmt.Fprintf(&b, "command = %s\n", tomlString(s.Command))
		b.WriteString("args = [")
		for i, a := range s.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tomlString(a))
		}
		b.WriteString("]\n")
		if len(s.Env) == 0 {
			continue
		}
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", s.Name)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", tomlString(k), tomlString(s.Env[k]))
		}
	}
	return b.String()
}

// tomlString quotes a value as a TOML basic string. Environment values
// carry tokens and paths, so the escaping has to be exact rather than
// hopeful: a stray quote or backslash would make the whole config
// unparseable and the session would start with no servers at all.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// copyFile copies src to dst with owner-only permissions and returns what
// it copied, which is the baseline the write-back compares against. A src
// that does not exist is not an error — an operator who has never logged
// in has no auth.json — and yields no bytes, which turns the write-back
// off.
func copyFile(src, dst string) ([]byte, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return b, os.WriteFile(dst, b, 0o600)
}

// specRoot is the workspace whose .mcp.json governs the session. The
// runner sets Cwd to the workspace root and MCPConfig to the .mcp.json in
// it; the config path wins when both are set, so a workspace that moves
// its file does not silently lose its servers.
func specRoot(cwd, mcpConfig string) string {
	if mcpConfig != "" {
		return filepath.Dir(mcpConfig)
	}
	return cwd
}

// doctorSetting is the workspace root and mcp.workspaceOnly value the
// Doctor row reports under. internal/app hands both in (see
// provider.ConfigDoctor); the fallback, for a caller with no workspace
// configuration at all, walks up from the working directory for a .sirdar
// and assumes the config's own default, which is on.
func doctorSetting(cfg *provider.DoctorConfig) (root string, only bool) {
	if cfg != nil {
		return cfg.Root, cfg.MCPWorkspaceOnly
	}
	root = doctorRoot(workingDir())
	if root == "" {
		root = workingDir()
	}
	return root, true
}

// doctorRoot finds the workspace Doctor is being run against by walking up
// from dir for the .sirdar directory that marks one. It falls back to dir
// itself, which is what `sirdar doctor` run from the workspace root gives.
func doctorRoot(dir string) string {
	for {
		if st, err := os.Stat(filepath.Join(dir, ".sirdar")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
