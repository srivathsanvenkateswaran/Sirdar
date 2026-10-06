package gather

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// fileHeader is what opens every gathered file: what ran, on what, how
// long it took and how it ended, so a reader of the file alone can tell
// what they are looking at.
type fileHeader struct {
	Name   string
	Call   string
	Values string
	TookMs int64
	Status string
	Reason string
}

// renderFile lays out one gathered file: the header, then the output in a
// fence no backtick run inside it can close, then the truncation marker
// when the output was cut.
func renderFile(h fileHeader, output string, total int) []byte {
	var b strings.Builder
	b.WriteString("# " + h.Name + "\n\n")
	b.WriteString("- Call: " + codeSpan(h.Call) + "\n")
	if h.Values != "" {
		b.WriteString("- Values: " + h.Values + "\n")
	}
	fmt.Fprintf(&b, "- Took: %d ms\n", h.TookMs)
	b.WriteString("- Status: " + h.Status + "\n")
	if h.Reason != "" {
		b.WriteString("- Reason: " + h.Reason + "\n")
	}
	b.WriteString("\n")
	f := fence(output)
	b.WriteString(f + "\n")
	b.WriteString(output)
	if output != "" && !strings.HasSuffix(output, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(f + "\n")
	if total > len(output) {
		b.WriteString(truncationMarker(len(output), total) + "\n")
	}
	return []byte(b.String())
}

// truncationMarker is the line that says a file holds only the start of
// what the step produced.
func truncationMarker(kept, total int) string {
	return fmt.Sprintf("[truncated by Sirdar at %d of %d bytes]", kept, total)
}

// fence is three backticks, lengthened while the text contains it — the
// rule the prompt's Slack section follows — so a code block inside a
// tool's output cannot close the one around it.
func fence(text string) string {
	f := "```"
	for strings.Contains(text, f) {
		f += "`"
	}
	return f
}

// codeSpan renders s as inline code, with a delimiter longer than any
// backtick run inside it.
func codeSpan(s string) string {
	d := "`"
	for strings.Contains(s, d) {
		d += "`"
	}
	if strings.Contains(s, "`") {
		return d + " " + s + " " + d
	}
	return d + s + d
}

// isFence reports whether line is a fence line: three or more backticks
// and nothing else.
func isFence(line string) bool {
	return len(line) >= 3 && strings.Trim(line, "`") == ""
}

// ReadOutput returns the output a file this package wrote carries: the
// text between its fence and the matching closing fence.
func ReadOutput(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	open := ""
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if open == "" {
			if isFence(line) {
				open = line
			}
			continue
		}
		if line == open {
			return strings.Join(lines, "\n"), nil
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("gather: %s has no fenced output", path)
}

// fileSafe makes s usable as part of a file name on every platform: every
// byte outside [A-Za-z0-9._-] becomes "_", and so do leading dots, so a
// value can neither climb out of the directory nor hide the file.
func fileSafe(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		case c == '.':
		default:
			b[i] = '_'
		}
	}
	for i := 0; i < len(b) && b[i] == '.'; i++ {
		b[i] = '_'
	}
	return string(b)
}

// capture keeps the first max bytes written to it and counts the rest,
// so a command that prints a whole table is cut, not buffered.
type capture struct {
	max   int
	buf   []byte
	total int
}

func (c *capture) Write(p []byte) (int, error) {
	c.total += len(p)
	if room := c.max - len(c.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		c.buf = append(c.buf, p[:room]...)
	}
	return len(p), nil
}

// kept is the captured text, cut back to a rune boundary when the cap
// fell inside a character.
func (c *capture) kept() string {
	return clipBytes(string(c.buf), len(c.buf), c.total > len(c.buf))
}

// clipBytes cuts s to at most n bytes, on a rune boundary when cut says
// the text was cut at all.
func clipBytes(s string, n int, cut bool) string {
	if len(s) > n {
		s, cut = s[:n], true
	}
	if !cut {
		return s
	}
	i := len(s) - 1
	for i > 0 && len(s)-i < utf8.UTFMax && !utf8.RuneStart(s[i]) {
		i--
	}
	if i >= 0 && !utf8.FullRuneInString(s[i:]) {
		s = s[:i]
	}
	return s
}

// tail keeps the last max bytes written to it: the end of a failing
// command's stderr is where the reason is.
type tail struct {
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

// firstLine is the first non-blank line of s, trimmed and clipped to n
// runes.
func firstLine(s string, n int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clipRunes(line, n)
		}
	}
	return ""
}

// clipRunes cuts s to n runes, marking the cut with an ellipsis.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
