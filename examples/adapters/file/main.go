// Command file-adapter is a reference implementation of a Sirdar source
// adapter: it serves the stdio protocol described in docs/adapters.md
// against a single JSON fixture file, rather than a real tracker or
// helpdesk API. It is used by internal/source/plugin's tests and by
// `sirdar --dry-run` in CI.
//
// Usage:
//
//	file-adapter -file testdata/tickets.json
//
// or with SIRDAR_FILE_ADAPTER set instead of -file. The fixture shape is:
//
//	{
//	  "tracker": {"OMNI-1": {...ticket.TrackerTicket...}},
//	  "helpdesk": {"555": {
//	    "ticket": {...ticket.HelpdeskTicket...},
//	    "thread": [...ticket.Message...],
//	    "attachments": [{"id":"a1","name":"shot.png","mime":"image/png","content_base64":"..."}]
//	  }}
//	}
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// fixture is the on-disk shape this adapter serves from.
type fixture struct {
	Tracker  map[string]ticket.TrackerTicket `json:"tracker"`
	Helpdesk map[string]helpdeskEntry        `json:"helpdesk"`
}

type helpdeskEntry struct {
	Ticket      ticket.HelpdeskTicket `json:"ticket"`
	Thread      ticket.Thread         `json:"thread"`
	Attachments []attachmentFixture   `json:"attachments"`
}

// attachmentFixture is an attachment's fixture-only representation: the
// content is inline base64 rather than a Path, since the adapter is what
// materialises it on disk on request.
type attachmentFixture struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MIME          string `json:"mime"`
	ContentBase64 string `json:"content_base64"`
}

func main() {
	file := flag.String("file", "", "path to the JSON fixture (or set SIRDAR_FILE_ADAPTER)")
	flag.Parse()

	path := *file
	if path == "" {
		path = os.Getenv("SIRDAR_FILE_ADAPTER")
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "file-adapter: -file or SIRDAR_FILE_ADAPTER is required")
		os.Exit(1)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "file-adapter: %v\n", err)
		os.Exit(1)
	}
	var fx fixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		fmt.Fprintf(os.Stderr, "file-adapter: parse %s: %v\n", path, err)
		os.Exit(1)
	}

	serve(fx, os.Stdin, os.Stdout)
}

// serve reads one Request per line from r and writes one Response per
// line to w, until r hits EOF or a "shutdown" request is handled.
func serve(fx fixture, r io.Reader, w io.Writer) {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	out := bufio.NewWriter(w)
	defer out.Flush()

	for in.Scan() {
		var req plugin.Request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue // tolerate noise on stdin
		}

		resp := dispatch(fx, req)
		b, err := json.Marshal(resp)
		if err != nil {
			b, _ = json.Marshal(errResp(req.ID, source.Internal, err.Error()))
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()

		if req.Method == "shutdown" {
			return
		}
	}
}

func dispatch(fx fixture, req plugin.Request) plugin.Response {
	switch req.Method {
	case "describe":
		return result(req.ID, plugin.Describe{Name: "file", Roles: []string{"tracker", "helpdesk"}, Version: "1"})

	case "tracker.get":
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, source.Internal, err.Error())
		}
		tt, ok := fx.Tracker[p.Key]
		if !ok {
			return errResp(req.ID, source.NotFound, "tracker ticket not found: "+p.Key)
		}
		return result(req.ID, tt)

	case "tracker.list":
		// Filters are accepted but ignored: the file adapter always
		// returns the full fixture.
		list := make([]ticket.TrackerTicket, 0, len(fx.Tracker))
		for _, tt := range fx.Tracker {
			list = append(list, tt)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
		return result(req.ID, list)

	case "helpdesk.get":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, source.Internal, err.Error())
		}
		e, ok := fx.Helpdesk[p.ID]
		if !ok {
			return errResp(req.ID, source.NotFound, "helpdesk ticket not found: "+p.ID)
		}
		return result(req.ID, e.Ticket)

	case "helpdesk.threads":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, source.Internal, err.Error())
		}
		e, ok := fx.Helpdesk[p.ID]
		if !ok {
			return errResp(req.ID, source.NotFound, "helpdesk ticket not found: "+p.ID)
		}
		return result(req.ID, e.Thread)

	case "helpdesk.attachments":
		var p struct {
			ID  string `json:"id"`
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, source.Internal, err.Error())
		}
		e, ok := fx.Helpdesk[p.ID]
		if !ok {
			return errResp(req.ID, source.NotFound, "helpdesk ticket not found: "+p.ID)
		}
		atts, err := writeAttachments(e.Attachments, p.Dir)
		if err != nil {
			return errResp(req.ID, source.Internal, err.Error())
		}
		return result(req.ID, atts)

	case "shutdown":
		return plugin.Response{ID: req.ID, Result: json.RawMessage("null")}

	default:
		return errResp(req.ID, source.Unsupported, "unknown method "+req.Method)
	}
}

// writeAttachments decodes each fixture attachment and writes it into dir
// as "<1-based index>-<name>", creating dir if needed. The returned
// Attachment.Path is relative to the bundle directory (dir's parent):
// "<base of dir>/<index>-<name>". An attachment that fails to decode or
// write is skipped, logged to stderr, and does not stop the rest from
// being returned — matching the partial-failure behaviour docs/adapters.md
// asks adapters to provide, since Sirdar treats Attachments errors as
// fatal but a short result as merely a warning.
func writeAttachments(fixtures []attachmentFixture, dir string) ([]ticket.Attachment, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	bundleRel := filepath.Base(dir)

	atts := make([]ticket.Attachment, 0, len(fixtures))
	for i, af := range fixtures {
		data, err := base64.StdEncoding.DecodeString(af.ContentBase64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "file-adapter: skipping attachment %s: decode: %v\n", af.ID, err)
			continue
		}
		safe := safeName(af.Name)
		name := fmt.Sprintf("%d-%s", i+1, safe)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "file-adapter: skipping attachment %s: write: %v\n", af.ID, err)
			continue
		}
		atts = append(atts, ticket.Attachment{
			ID:   af.ID,
			Name: safe,
			MIME: af.MIME,
			Path: bundleRel + "/" + name,
		})
	}
	return atts, nil
}

// safeName reduces an attachment's name to a single filename component, so
// joining it onto the destination directory cannot land the file somewhere
// else: a fixture naming an attachment "../../.ssh/authorized_keys" would
// otherwise be written wherever that resolves to. Directory parts, path
// separators and control characters are dropped; a name left with nothing
// usable becomes "attachment".
func safeName(name string) string {
	var b strings.Builder
	for _, r := range filepath.Base(filepath.FromSlash(name)) {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		return "attachment"
	}
	return clean
}

func result(id int, v any) plugin.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return errResp(id, source.Internal, err.Error())
	}
	return plugin.Response{ID: id, Result: b}
}

func errResp(id int, code source.Code, msg string) plugin.Response {
	return plugin.Response{ID: id, Error: &source.Error{Code: code, Message: msg}}
}
