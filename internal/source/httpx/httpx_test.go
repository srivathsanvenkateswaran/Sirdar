package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- redirect policy ---

// TestRedirectPolicyStopsAtTheHopCap: a server that redirects to itself for
// ever is stopped by the hop cap, not by the transport running out of
// patience.
func TestRedirectPolicyStopsAtTheHopCap(t *testing.T) {
	t.Parallel()
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	tr := mustTrust(t, srv.URL)
	hc := Client(srv.Client(), tr, 3)
	_, err := hc.Get(srv.URL + "/start")
	if err == nil {
		t.Fatal("Get through an endless redirect chain succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "stopped after 3 redirects") {
		t.Errorf("error = %v, want it to name the hop cap", err)
	}
	mu.Lock()
	defer mu.Unlock()
	// via already holds the original request when the first redirect is
	// checked, so a cap of three stops the third hop: the original plus two.
	if hits != 3 {
		t.Errorf("server saw %d requests, want 3 (the original and two hops)", hits)
	}
}

// TestRedirectPolicyRefusesAnOffHostHop: the foreign listener fails the test
// if it is ever called. Go strips Authorization on a cross-host hop but
// still makes the request; refusing the hop is what stops the response
// reaching the caller at all.
func TestRedirectPolicyRefusesAnOffHostHop(t *testing.T) {
	t.Parallel()
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect target was contacted: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(foreign.Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/signin?RelayState=secret", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	tr := mustTrust(t, srv.URL)
	hc := Client(srv.Client(), tr, 3)
	resp, err := hc.Get(srv.URL + "/attachment")
	if err == nil {
		resp.Body.Close()
		t.Fatal("Get followed a redirect off the trusted host, want an error")
	}
	if !strings.Contains(err.Error(), ReasonNotTrusted) {
		t.Errorf("error = %v, want it to say the host was not trusted", err)
	}
	// Go wraps the refusal in a *url.Error carrying the target URL, query
	// and all, which is why a caller whose message reaches a log or a prompt
	// uses RedirectPolicyStop and names the host itself.
}

// TestRedirectPolicyStopHandsBackTheRedirect: an untrusted hop stops the
// chain without an error, so the caller sees the 3xx and can report where it
// was being sent without quoting the query.
func TestRedirectPolicyStopHandsBackTheRedirect(t *testing.T) {
	t.Parallel()
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect target was contacted: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(foreign.Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/signin?RelayState=secret", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	hc := *srv.Client()
	hc.CheckRedirect = RedirectPolicyStop(mustTrust(t, srv.URL), 3)
	resp, err := hc.Get(srv.URL + "/attachment")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want the 302 handed back", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc.Host != strings.TrimPrefix(foreign.URL, "http://") {
		t.Errorf("Location host = %q, want the refused target", loc.Host)
	}
}

func TestClientPreservesTransportAndTimeout(t *testing.T) {
	t.Parallel()
	base := &http.Client{Timeout: 7 * time.Second, Transport: http.DefaultTransport}
	got := Client(base, mustTrust(t, "https://example.com"), 3)
	if got == base {
		t.Error("Client returned the caller's own client, want a copy")
	}
	if got.Timeout != base.Timeout {
		t.Errorf("Timeout = %v, want %v", got.Timeout, base.Timeout)
	}
	if got.Transport != base.Transport {
		t.Error("Transport was not preserved")
	}
	if got.CheckRedirect == nil {
		t.Error("CheckRedirect was not set")
	}
	if base.CheckRedirect != nil {
		t.Error("the caller's client was modified")
	}
	if Client(nil, nil, 0) == nil {
		t.Error("Client(nil, nil, 0) = nil, want a usable client")
	}
}

// --- Retry-After ---

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	max := 30 * time.Second
	tests := []struct {
		name   string
		header string
		set    bool
		want   time.Duration
		wantOK bool
	}{
		{name: "absent", set: false},
		{name: "empty", header: "", set: true},
		{name: "seconds", header: "5", set: true, want: 5 * time.Second, wantOK: true},
		{name: "zero seconds", header: "0", set: true, want: 0, wantOK: true},
		{name: "at the ceiling", header: "30", set: true, want: 30 * time.Second, wantOK: true},
		{name: "over the ceiling", header: "31", set: true},
		{name: "an hour", header: "3600", set: true},
		{name: "negative", header: "-1", set: true},
		{name: "not a number", header: "soon", set: true},
		{name: "padded", header: "  7  ", set: true, want: 7 * time.Second, wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tc.set {
				h.Set("Retry-After", tc.header)
			}
			got, ok := RetryAfter(h, max)
			if ok != tc.wantOK {
				t.Fatalf("RetryAfter(%q) ok = %v, want %v", tc.header, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("RetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// TestRetryAfterHTTPDate covers the header's second documented form. The
// duration is relative to now, so it is checked as a range.
func TestRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	max := 30 * time.Second

	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))
	got, ok := RetryAfter(h, max)
	if !ok {
		t.Fatal("an HTTP-date five seconds out was refused")
	}
	if got < 3*time.Second || got > 6*time.Second {
		t.Errorf("RetryAfter = %v, want about 5s", got)
	}

	// A date already past is "now", not a refusal.
	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if got, ok := RetryAfter(h, max); !ok || got != 0 {
		t.Errorf("a past date = (%v, %v), want (0, true)", got, ok)
	}

	// A date past the ceiling is refused like an over-long delta.
	h.Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	if _, ok := RetryAfter(h, max); ok {
		t.Error("an hour-long HTTP-date wait was accepted, want it refused")
	}
}

func TestSleepCtx(t *testing.T) {
	t.Parallel()
	if err := SleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("SleepCtx: %v", err)
	}
	// A non-positive duration reports the context rather than sleeping.
	if err := SleepCtx(context.Background(), 0); err != nil {
		t.Errorf("SleepCtx(0) = %v, want nil", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepCtx(cancelled, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("SleepCtx(cancelled, 0) = %v, want context.Canceled", err)
	}
	if err := SleepCtx(cancelled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("SleepCtx(cancelled, 1h) = %v, want context.Canceled", err)
	}
}

// --- ReadLimited ---

func TestReadLimited(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		max     int64
		want    string
		wantErr bool
	}{
		{name: "well inside", body: "hello", max: 16, want: "hello"},
		{name: "exactly at the limit", body: "hello", max: 5, want: "hello"},
		{name: "one byte over", body: "hello", max: 4, want: "hell", wantErr: true},
		{name: "empty body", body: "", max: 4, want: ""},
		{name: "zero limit", body: "x", max: 0, want: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReadLimited(strings.NewReader(tc.body), tc.max)
			if tc.wantErr {
				if !errors.Is(err, ErrTooLarge) {
					t.Fatalf("err = %v, want ErrTooLarge", err)
				}
			} else if err != nil {
				t.Fatalf("ReadLimited: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("ReadLimited = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadLimitedPropagatesAReadError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	if _, err := ReadLimited(io.MultiReader(strings.NewReader("ab"), errReader{want}), 100); !errors.Is(err, want) {
		t.Errorf("err = %v, want the reader's own error", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// --- Download ---

func TestDownload(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG fake bytes"))
		case "/big":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><form id=\"login\"></form></html>"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no such attachment"}`))
		}
	}))
	t.Cleanup(srv.Close)

	get := func(t *testing.T, path string) *http.Request {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		return req
	}

	t.Run("writes the file and reports the content type", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "shot.png")
		ct, err := Download(context.Background(), srv.Client(), get(t, "/file"), dest, DownloadOptions{Max: 1 << 20})
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if ct != "image/png" {
			t.Errorf("content type = %q, want image/png", ct)
		}
		b, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(b) != "\x89PNG fake bytes" {
			t.Errorf("file = %q, want the served bytes", b)
		}
		assertNoLeftovers(t, filepath.Dir(dest), "shot.png")
	})

	t.Run("refuses a body past the ceiling and leaves nothing behind", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "big.bin")
		_, err := Download(context.Background(), srv.Client(), get(t, "/big"), dest, DownloadOptions{Max: 10})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Errorf("the oversize download was left on disk (stat err = %v)", statErr)
		}
		assertNoLeftovers(t, dir)
	})

	t.Run("a body exactly at the ceiling is kept", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "exact.bin")
		if _, err := Download(context.Background(), srv.Client(), get(t, "/big"), dest, DownloadOptions{Max: 100}); err != nil {
			t.Fatalf("Download: %v", err)
		}
		fi, err := os.Stat(dest)
		if err != nil || fi.Size() != 100 {
			t.Errorf("stat = %v (err %v), want a 100 byte file", fi, err)
		}
	})

	t.Run("refuses an html body when a file was expected", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "login.png")
		_, err := Download(context.Background(), srv.Client(), get(t, "/login"), dest, DownloadOptions{Max: 1 << 20, RefuseHTML: true})
		if !errors.Is(err, ErrHTMLBody) {
			t.Fatalf("err = %v, want ErrHTMLBody", err)
		}
		if !strings.Contains(err.Error(), "text/html") {
			t.Errorf("err = %v, want it to name the content type", err)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Error("a sign-in page was written to disk under the attachment's name")
		}
		assertNoLeftovers(t, dir)
	})

	t.Run("html is kept when the caller did not ask for a binary", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "page.html")
		if _, err := Download(context.Background(), srv.Client(), get(t, "/login"), dest, DownloadOptions{Max: 1 << 20}); err != nil {
			t.Fatalf("Download: %v", err)
		}
	})

	t.Run("a non-2xx response is a StatusError carrying the body", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "missing.bin")
		_, err := Download(context.Background(), srv.Client(), get(t, "/missing"), dest, DownloadOptions{Max: 1 << 20})
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("err = %v, want a *StatusError", err)
		}
		if se.Status != http.StatusNotFound {
			t.Errorf("status = %d, want 404", se.Status)
		}
		if !strings.Contains(string(se.Body), "no such attachment") {
			t.Errorf("body = %q, want the served error document", se.Body)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Error("a failed download left a file behind")
		}
		assertNoLeftovers(t, dir)
	})

	t.Run("a transport failure leaves nothing behind", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "gone.bin")
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/x", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if _, err := Download(context.Background(), &http.Client{Timeout: 2 * time.Second}, req, dest, DownloadOptions{Max: 1 << 20}); err == nil {
			t.Fatal("Download to a dead listener succeeded, want an error")
		}
		assertNoLeftovers(t, dir)
	})
}

// assertNoLeftovers checks that dir holds exactly the named files: a
// download that failed halfway must not leave its temporary file behind
// either.
func assertNoLeftovers(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("dir holds %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dir holds %v, want %v", got, want)
		}
	}
}

func TestIsHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/HTML", true},
		{"application/xhtml+xml", true},
		{"text/html;", true}, // unparseable parameters, still html
		{"image/png", false},
		{"application/json", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsHTML(tc.ct); got != tc.want {
			t.Errorf("IsHTML(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

// --- SanitizeName ---

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{name: "plain", in: "screenshot.png", want: "screenshot.png"},
		{name: "spaces are kept", in: "crash log.txt", want: "crash log.txt"},
		{name: "traversal", in: "../../etc/passwd", want: "passwd"},
		{name: "absolute path", in: "/absolute/path/log.txt", want: "log.txt"},
		{name: "windows traversal", in: `..\..\windows\evil.exe`, want: "evil.exe"},
		{name: "empty", in: "", want: "attachment"},
		{name: "dot", in: ".", want: "attachment"},
		{name: "dotdot", in: "..", want: "attachment"},
		{name: "slash", in: "/", want: "attachment"},
		{name: "all spaces", in: "   ", want: "attachment"},
		{name: "nul byte", in: "a\x00b.txt", want: "ab.txt"},
		{name: "tab", in: "tab\there.txt", want: "tabhere.txt"},
		{name: "del", in: "del\x7fhere.txt", want: "delhere.txt"},
		{name: "newline", in: "two\nlines.txt", want: "twolines.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeName(tc.in); got != tc.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeNameCapsLongNames: the cap is in bytes, but it never splits a
// rune and it keeps the extension, so a long non-ASCII name — Arabic here,
// which is what a Zoho Desk ticket actually carries — stays readable and
// stays a .png.
func TestSanitizeNameCapsLongNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ascii", in: strings.Repeat("x", 200) + ".png", want: strings.Repeat("x", 116) + ".png"},
		{name: "two-byte runes", in: strings.Repeat("é", 200) + ".png", want: strings.Repeat("é", 58) + ".png"},
		{name: "rtl arabic", in: strings.Repeat("ش", 200) + ".png", want: strings.Repeat("ش", 58) + ".png"},
		{name: "no extension", in: strings.Repeat("y", 200), want: strings.Repeat("y", 120)},
		{name: "extension longer than the cap", in: "a." + strings.Repeat("z", 200), want: "a." + strings.Repeat("z", 118)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeName(tc.in)
			if got != tc.want {
				t.Errorf("SanitizeName(%q…) = %q…, want %q…", tc.in[:16], got, tc.want)
			}
			if len(got) > MaxNameBytes {
				t.Errorf("SanitizeName kept %d bytes, want at most %d", len(got), MaxNameBytes)
			}
			if !utf8Valid(got) {
				t.Errorf("SanitizeName(%q…) = %q, which is not valid UTF-8", tc.in[:16], got)
			}
		})
	}
}

// TestSanitizeNameKeepsRTLMarkersButNeverAPath: a right-to-left override is
// a display trick, not a path component. It survives (the name is still the
// one the API served), and the rune is never split by the byte cap.
func TestSanitizeNameKeepsRTLMarkersButNeverAPath(t *testing.T) {
	t.Parallel()
	const rtlOverride = "‮"
	in := "../" + rtlOverride + "gnp.exe"
	got := SanitizeName(in)
	if strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Errorf("SanitizeName(%q) = %q, want no path left in it", in, got)
	}
	if !strings.Contains(got, rtlOverride) {
		t.Errorf("SanitizeName(%q) = %q, want the name served by the API", in, got)
	}
	long := strings.Repeat("مرحبا"+rtlOverride, 40) + ".pdf"
	capped := SanitizeName(long)
	if len(capped) > MaxNameBytes || !utf8Valid(capped) || !strings.HasSuffix(capped, ".pdf") {
		t.Errorf("SanitizeName of a long RTL name = %q (%d bytes)", capped, len(capped))
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// --- Warnings ---

func TestWarningsAppendDedupeAndTake(t *testing.T) {
	t.Parallel()
	var w Warnings

	if got := w.Take("SUP-1"); got != nil {
		t.Errorf("Take on an empty store = %v, want nil", got)
	}

	w.Add("SUP-1", "threads: page cap reached")
	w.Add("SUP-1", "threads: page cap reached", "attachments: host not trusted")
	w.Add("SUP-2", "another ticket's problem")
	w.Add("SUP-1") // no lines at all is a no-op

	got := w.Take("SUP-1")
	want := []string{"threads: page cap reached", "attachments: host not trusted"}
	if len(got) != len(want) {
		t.Fatalf("Take = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Take = %v, want %v (order is the order they were recorded)", got, want)
		}
	}
	// Reading is what clears the entry.
	if again := w.Take("SUP-1"); again != nil {
		t.Errorf("second Take = %v, want nil", again)
	}
	// One ticket's read never touches another's.
	if other := w.Take("SUP-2"); len(other) != 1 {
		t.Errorf("Take(SUP-2) = %v, want the one line recorded for it", other)
	}
	// The returned slice is a copy: mutating it cannot corrupt the store.
	w.Add("SUP-3", "one")
	taken := w.Take("SUP-3")
	taken[0] = "mutated"
	w.Add("SUP-3", "one")
	if again := w.Take("SUP-3"); len(again) != 1 || again[0] != "one" {
		t.Errorf("Take = %v, want the store to be unaffected by a caller's edit", again)
	}
}

// TestWarningsConcurrent is the reason the store is keyed by ticket: one
// Client serves every ticket in a run, and two of them finish at once. Run
// with -race.
func TestWarningsConcurrent(t *testing.T) {
	t.Parallel()
	var w Warnings
	const tickets, writers = 8, 8

	var wg sync.WaitGroup
	for i := 0; i < tickets; i++ {
		id := fmt.Sprintf("T-%d", i)
		for j := 0; j < writers; j++ {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				w.Add(id, fmt.Sprintf("%s: warning %d", id, j), id+": shared line")
			}(j)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Take(id)
		}()
	}
	wg.Wait()

	// Whatever survived the interleaving must belong to its own ticket and
	// carry no duplicate.
	for i := 0; i < tickets; i++ {
		id := fmt.Sprintf("T-%d", i)
		seen := map[string]bool{}
		for _, line := range w.Take(id) {
			if !strings.HasPrefix(line, id+":") {
				t.Errorf("%s picked up another ticket's warning: %q", id, line)
			}
			if seen[line] {
				t.Errorf("%s recorded %q twice", id, line)
			}
			seen[line] = true
		}
	}
}

// --- List bounds ---

func TestLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		requested  int
		want       int
		wantCapped bool
	}{
		{name: "unset", requested: 0, want: 100},
		{name: "negative", requested: -5, want: 100},
		{name: "under the ceiling", requested: 50, want: 50},
		{name: "at the ceiling", requested: 200, want: 200},
		{name: "over the ceiling", requested: 500, want: 200, wantCapped: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, capped := Limit(tc.requested, 100, 200)
			if got != tc.want || capped != tc.wantCapped {
				t.Errorf("Limit(%d, 100, 200) = (%d, %v), want (%d, %v)", tc.requested, got, capped, tc.want, tc.wantCapped)
			}
		})
	}
}

func TestPageSize(t *testing.T) {
	t.Parallel()
	tests := []struct{ n, apiMax, want int }{
		{0, 100, 100},
		{-1, 100, 100},
		{10, 100, 10},
		{100, 100, 100},
		{250, 100, 100},
	}
	for _, tc := range tests {
		if got := PageSize(tc.n, tc.apiMax); got != tc.want {
			t.Errorf("PageSize(%d, %d) = %d, want %d", tc.n, tc.apiMax, got, tc.want)
		}
	}
}

var _ = url.Parse
