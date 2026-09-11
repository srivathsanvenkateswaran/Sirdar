package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// webTimeout bounds one fetch, including redirects and body read.
var webTimeout = 20 * time.Second

// maxFetchBytes is the hard cap on a response body, applied before any
// output truncation.
const maxFetchBytes = 1 << 20

// maxRedirects bounds a redirect chain; every hop must stay on the host
// the model asked for.
const maxRedirects = 5

var webFetchSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "url": {"type": "string", "description": "Absolute http or https URL to fetch with GET."}
  },
  "required": ["url"]
}`)

func (o Options) webFetchTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "web_fetch",
			Description: "Fetch a URL with GET and return its text. Only http and https, only text, JSON or XML responses, at most 1 MiB, and redirects must stay on the original host. HTML is reduced to plain text.",
			Parameters:  webFetchSchema,
		},
		call: o.webFetch,
	}
}

func (o Options) webFetch(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	raw := strings.TrimSpace(a.URL)
	if raw == "" {
		return "", errors.New("web_fetch: url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("web_fetch: invalid url: %v", err)
	}
	if u.Scheme == "" {
		return "", errors.New("web_fetch: url must be absolute, e.g. https://example.com/page")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch: unsupported scheme %q: only http and https are allowed", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("web_fetch: url must be absolute, e.g. https://example.com/page")
	}

	reqCtx, cancel := context.WithTimeout(ctx, webTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %v", err)
	}
	req.Header.Set("User-Agent", "sirdar/agenttools")
	req.Header.Set("Accept", "text/*, application/json, application/xml")

	resp, err := o.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("web_fetch: %s returned %s", u.Redacted(), resp.Status)
	}

	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(resp.Header.Get("Content-Type")))
	}
	mediaType = strings.ToLower(mediaType)
	if !allowedMediaType(mediaType) {
		return "", fmt.Errorf("web_fetch: unsupported content type %q: only text/*, application/json and application/xml are allowed", mediaType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch: %v", err)
	}
	capped := false
	if len(body) > maxFetchBytes {
		body = body[:maxFetchBytes]
		capped = true
	}

	text := string(body)
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		text = stripHTML(text)
	}
	out, cut := truncate(text, o.MaxOutputBytes)
	if capped && !cut {
		out += "\n[truncated at 1 MiB]"
	}
	return out, nil
}

func allowedMediaType(mediaType string) bool {
	switch {
	case strings.HasPrefix(mediaType, "text/"):
		return true
	case mediaType == "application/json", mediaType == "application/xml":
		return true
	default:
		return false
	}
}

// allowPrivateAddrs reports whether the private-address guard should stand
// aside. It answers false. The only thing that ever replaces it is a test
// in this package: an httptest server necessarily listens on loopback,
// which is the first address the guard refuses.
//
// It is a func rather than a bool so the escape hatch cannot be opened by
// assigning a value read from configuration, a flag or the environment.
// Reaching it takes code in this package, which is where the reasoning
// about what web_fetch may connect to belongs.
var allowPrivateAddrs = func() bool { return false }

// guardAddress is the dialer hook that refuses a connection to an address
// the model must not reach through web_fetch: loopback, RFC1918, carrier
// NAT, link-local — which is where cloud metadata services live, notably
// 169.254.169.254 — unique-local IPv6, the unspecified address, and
// multicast. It runs on the resolved IP, not the hostname, so a public DNS
// name pointed at an internal address is refused too, and it sits on the
// dialer rather than in the tool so a redirect chain is checked hop by hop.
func guardAddress(_, address string, _ syscall.RawConn) error {
	if allowPrivateAddrs() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing to connect to %s: unresolved address", address)
	}
	if blockedIP(ip) {
		return fmt.Errorf("refusing to connect to %s: private, loopback and link-local addresses are not fetchable", ip)
	}
	return nil
}

// blockedIP reports whether an address falls in one of the ranges
// web_fetch refuses. net.IP.IsPrivate covers both RFC1918 and IPv6
// unique-local (fc00::/7).
func blockedIP(ip net.IP) bool {
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsPrivate():
		return true
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return true
	}
	// 100.64.0.0/10, carrier-grade NAT: not routed on the public internet.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// httpClient builds the client web_fetch uses: a transport of its own,
// whose dialer refuses private addresses, plus the redirect rule that keeps
// a chain on the host the model asked for. A caller-supplied client
// contributes its TLS configuration, cookie jar and timeout — an httptest
// TLS server needs the first — but never its dialer, which is what keeps
// the address guard in force on every path. The caller's client is never
// mutated.
func (o Options) httpClient() *http.Client {
	client := &http.Client{Timeout: webTimeout}
	transport := &http.Transport{
		// No proxy, deliberately. The address guard runs on the dialer,
		// so it sees whatever the transport connects to — and through a
		// proxy that is the proxy, every time, whatever the model asked
		// for. HTTP_PROXY in the environment would therefore turn the
		// guard off without anyone deciding to, and 169.254.169.254
		// would be one fetch away again. Connecting directly keeps the
		// address the guard checks and the address the fetch reaches the
		// same one; a workspace that can only reach the internet through
		// a proxy loses web_fetch, and gets a connection error rather
		// than a silently unguarded fetch.
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   guardAddress,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if o.HTTP != nil {
		if t, ok := o.HTTP.Transport.(*http.Transport); ok && t != nil {
			transport.TLSClientConfig = t.TLSClientConfig
		}
		client.Jar = o.HTTP.Jar
		if o.HTTP.Timeout > 0 {
			client.Timeout = o.HTTP.Timeout
		}
	}
	client.Transport = transport
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		origin := via[0].URL.Host
		if !strings.EqualFold(req.URL.Host, origin) {
			return fmt.Errorf("refusing redirect off the original host (%s to %s)", origin, req.URL.Host)
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect to scheme %q", req.URL.Scheme)
		}
		return nil
	}
	return client
}

var (
	breakBefore = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/tr|/h[1-6]|/section|/article|/blockquote)\b[^>]*>`)
	anyTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRuns   = regexp.MustCompile(`\n{3,}`)
	spaceRuns   = regexp.MustCompile(`[ \t]{2,}`)
)

// dropElements removes elements whose content is markup, not prose. RE2
// has no back-references, so each element gets its own pattern.
var dropElements = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`),
	regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`),
	regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`),
	regexp.MustCompile(`(?is)<template\b[^>]*>.*?</template\s*>`),
}

// stripHTML reduces an HTML document to the text a model can read: script,
// style and template content dropped, block ends turned into newlines,
// remaining tags removed and entities decoded.
func stripHTML(s string) string {
	for _, re := range dropElements {
		s = re.ReplaceAllString(s, " ")
	}
	s = breakBefore.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = spaceRuns.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	s = strings.Join(lines, "\n")
	s = blankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
