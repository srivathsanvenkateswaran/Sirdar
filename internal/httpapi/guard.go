package httpapi

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// The API has no authentication: it answers to whoever can reach the
// listener, which on a loopback bind is every program on the machine — and
// every page the operator's browser has open. A page cannot read a response
// from another origin, but it can *send* a request: an HTML form posting to
// http://127.0.0.1:7777/api/workspaces/ws1/fix would start an agent session
// that writes code and opens a pull request, and the operator would see
// nothing but a tab that navigated away.
//
// Three checks close that, all of them on what the browser itself says
// about where the request came from:
//
//   - Content-Type must be application/json. A form post can only send
//     application/x-www-form-urlencoded, multipart/form-data or text/plain;
//     anything else needs fetch/XHR, which is already subject to CORS.
//   - Origin, when the browser sends one, must be this server's own or the
//     Wails shell's.
//   - Sec-Fetch-Site must not say the request came from another site.
//
// A caller with no browser in the way — curl, the CLI's own tests, a script
// the operator wrote — sends no Origin and no Sec-Fetch-Site and is
// unaffected, which is deliberate: this is a cross-site defence, not
// authentication, and nothing here stops a program already running as the
// operator.
//
// The hook routes are exempt. They authenticate each delivery by signature
// or shared secret, they are posted to by trackers rather than browsers,
// and their bodies are the tracker's own content type.

// wailsOrigins are the origins the Wails shell's webview loads the UI from:
// a custom scheme on macOS and Linux, and a mapped loopback host on Windows
// (see wails/v2 internal/frontend/desktop/*/frontend.go).
//
// The desktop app does not in fact reach this server — desktop/frontend
// picks createWailsTransport() whenever window.go.main.Bridge is bound, and
// calls the Go side in process — so nothing here is load-bearing for it
// today. They are allowed explicitly so that a shell which ever does fall
// back to the HTTP transport keeps working instead of failing with a 403
// nobody can place.
var wailsOrigins = map[string]bool{
	"wails://wails":           true, // macOS (WKWebView), Linux (WebKit2GTK)
	"http://wails.localhost":  true, // Windows (WebView2)
	"https://wails.localhost": true,
}

// LoopbackOnly tells the server whether its listener can be reached only
// from this machine. It is the bind decision `sirdar serve` made, not a
// guess this package could make from a request: the fix route writes code
// and opens pull requests, and that is refused when a remote caller could
// be the one asking.
//
// Without it the server assumes the listener is reachable and refuses the
// fix route, so a shell that never thought about the question fails closed.
func LoopbackOnly(v bool) Option {
	return func(s *server) { s.loopbackOnly = v }
}

// safeMethod reports whether the method only reads. These carry no guard:
// a GET has nothing to change, and the responses are already unreadable
// cross-origin without CORS headers, which this server never sends.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// guard runs the cross-site checks, writing the refusal itself and
// returning false when the request must not reach a handler.
func (s *server) guard(w http.ResponseWriter, r *http.Request) bool {
	if safeMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/hooks/") {
		return true
	}
	// Sec-Fetch-Site is the browser's own account of the relationship
	// between the page and this server. "same-site" is refused as well as
	// "cross-site": a sibling host that resolves to loopback is a different
	// origin, and nothing the operator runs needs to post from one.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		writeError(w, http.StatusForbidden, "forbidden",
			"this endpoint does not accept requests from another site")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.allowedOrigin(r, origin) {
		writeError(w, http.StatusForbidden, "forbidden",
			"this endpoint does not accept requests from "+origin)
		return false
	}
	return jsonContentType(w, r)
}

// allowedOrigin reports whether origin is one this server answers to: the
// origin it is being reached on, or the Wails shell's.
//
// Host and port must match exactly; the scheme need only be http or https.
// A browser reaching an operator's listener through a TLS-terminating
// reverse proxy sends https:// while the listener itself speaks plain HTTP,
// and refusing that would break the UI for no gain — an attacker's page
// cannot be served from this host and port whichever scheme it uses.
func (s *server) allowedOrigin(r *http.Request, origin string) bool {
	if wailsOrigins[strings.ToLower(origin)] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// "null" — a sandboxed iframe or a data: URL — lands here.
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// jsonContentType requires a body this API could have sent itself. A
// request with no body and no declared type is let through: cancel, the
// workspace delete and a resume that answers no question all send none.
func jsonContentType(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" && r.ContentLength == 0 {
		return true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"this endpoint accepts application/json only")
		return false
	}
	return true
}
