package openai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// transcriptFile is the name the loop persists its messages under, inside
// the run directory it was given.
const transcriptFile = "transcript.json"

// transcriptVersion is the on-disk format. A file written by a newer
// Sirdar is refused rather than half-read.
const transcriptVersion = 1

// transcript is what `provider: openai` resumes from. A CLI provider
// resumes by naming a session its own binary still holds; this loop has no
// such thing — the conversation lives in this process — so the
// conversation itself is what gets written down.
//
// What goes in the file is exactly what goes to the model: the system
// message, the user messages, the assistant turns and their tool results.
// No credential is among them. The API key travels in an Authorization
// header built from the workspace's config at request time and is never a
// message, so it cannot reach this file; the same goes for the MCP servers'
// environment, which the loop passes to the child process and never puts
// in the transcript. What the file does hold is everything the session
// read — ticket text, log lines, whatever a tool returned — so it is
// written 0600, like the run's other artefacts.
type transcript struct {
	Version  int       `json:"version"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Mode     string    `json:"mode"`
	SavedAt  time.Time `json:"savedAt"`
	Turns    int       `json:"turns"`
	Messages []Message `json:"messages"`
}

// closeOpenToolCalls answers any tool call the session did not get to.
//
// A session that was cancelled or ran out of budget mid-batch leaves an
// assistant message whose tool_calls have no answers, and an unanswered
// tool_call_id is a 400 from most endpoints on the next request — which
// would be the resumed session's first. Answering them here keeps the
// transcript one a model can be handed, and says plainly what happened.
func closeOpenToolCalls(msgs []Message) []Message {
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}

	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m)
		if m.Role != "assistant" {
			continue
		}
		for _, call := range m.ToolCalls {
			if call.ID == "" || answered[call.ID] {
				continue
			}
			answered[call.ID] = true
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    "interrupted: the session ended before this tool call was answered",
			})
		}
	}
	return out
}

// transcriptPath is where a session with this run directory keeps its
// transcript, or "" when the caller kept no run directory.
func transcriptPath(runDir string) string {
	if runDir == "" {
		return ""
	}
	return filepath.Join(runDir, transcriptFile)
}

// writeTranscript writes t to path, 0600, through a temporary file in the
// same directory so a crash mid-write cannot leave a half-written
// transcript where a resume would read it.
func writeTranscript(path string, t transcript) error {
	t.Messages = closeOpenToolCalls(t.Messages)
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("openai: marshal transcript: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".transcript-*.json")
	if err != nil {
		return fmt.Errorf("openai: create transcript: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename has succeeded

	// CreateTemp already makes the file 0600, but say so: the rename
	// carries the mode over, and a future change to that default must not
	// quietly widen this file.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("openai: chmod transcript: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("openai: write transcript: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("openai: write transcript: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("openai: replace transcript: %w", err)
	}
	return nil
}

// readTranscript loads a transcript written by an earlier session of the
// same run.
func readTranscript(path string) (transcript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return transcript{}, fmt.Errorf("openai: read transcript: %w", err)
	}
	var t transcript
	if err := json.Unmarshal(data, &t); err != nil {
		return transcript{}, fmt.Errorf("openai: parse transcript %s: %w", path, err)
	}
	if t.Version != transcriptVersion {
		return transcript{}, fmt.Errorf("openai: transcript %s is version %d, this build reads %d",
			path, t.Version, transcriptVersion)
	}
	if len(t.Messages) == 0 {
		return transcript{}, fmt.Errorf("openai: transcript %s carries no messages", path)
	}
	return t, nil
}
