package ticket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ThreadMarkdown renders the thread in original language with roles and RFC3339 times.
func ThreadMarkdown(t Thread, atts []Attachment) string {
	pathByID := make(map[string]string, len(atts))
	for _, a := range atts {
		pathByID[a.ID] = a.Path
	}

	var b strings.Builder
	b.WriteString("# Conversation (original language)\n\n")
	for _, m := range t {
		fmt.Fprintf(&b, "## %s · %s · %s\n\n", m.At.Format(time.RFC3339), m.Role, m.Author)
		b.WriteString(m.Text)
		b.WriteString("\n\n")
		if len(m.AttachmentIDs) > 0 {
			paths := make([]string, 0, len(m.AttachmentIDs))
			for _, id := range m.AttachmentIDs {
				if p, ok := pathByID[id]; ok {
					paths = append(paths, p)
				}
			}
			if len(paths) > 0 {
				fmt.Fprintf(&b, "Attachments: %s\n\n", strings.Join(paths, ", "))
			}
		}
	}
	return b.String()
}

// WriteBundle writes ticket.json, thread.md and copies nothing (attachments are already
// in dir/attachments because Helpdesk.Attachments downloaded them there).
func WriteBundle(dir string, b Bundle) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ticket.json"), raw, 0o644); err != nil {
		return err
	}

	md := ThreadMarkdown(b.Thread, b.Attachments)
	if err := os.WriteFile(filepath.Join(dir, "thread.md"), []byte(md), 0o644); err != nil {
		return err
	}

	return nil
}
