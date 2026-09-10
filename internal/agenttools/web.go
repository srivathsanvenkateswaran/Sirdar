package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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

// httpClient wraps the caller's transport in a client that refuses methods
// and redirects the tool must not follow. The caller's client is never
// mutated.
func (o Options) httpClient() *http.Client {
	client := &http.Client{Timeout: webTimeout}
	if o.HTTP != nil {
		client.Transport = o.HTTP.Transport
		client.Jar = o.HTTP.Jar
		if o.HTTP.Timeout > 0 {
			client.Timeout = o.HTTP.Timeout
		}
	}
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
