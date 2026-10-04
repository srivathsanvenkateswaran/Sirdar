package run

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// sessionSlugWords and sessionSlugMax bound the slug of a session key: the
// first few words of the instruction say what the session was about, and a
// run directory named after a whole paragraph would be unreadable in a
// listing.
const (
	sessionSlugWords = 5
	sessionSlugMax   = 40
)

// SessionKey is the key a session with no ticket reference is filed under.
func SessionKey(instruction string, now time.Time) string {
	words := strings.Fields(instruction)
	if len(words) > sessionSlugWords {
		words = words[:sessionSlugWords]
	}
	slug := note.Slug(strings.Join(words, " "))
	if len(slug) > sessionSlugMax {
		slug = strings.TrimRight(slug[:sessionSlugMax], "-")
	}
	// An instruction with no Latin letters or digits in its first words —
	// one typed in Arabic, say — slugs to nothing, and a key still needs a
	// name after its date.
	if slug == "" {
		slug = "session"
	}
	return "ASK-" + now.UTC().Format("20060102") + "-" + slug
}

// Session runs one session: an instruction, with a ticket reference when key names one.
// An empty key with o.NoBundle mints SessionKey(o.Instruction, r.now()).
func (r *Runner) Session(ctx context.Context, key string, o Options) (Outcome, error) {
	if r.Config == nil {
		return Outcome{}, fmt.Errorf("run: no workspace configuration")
	}
	// Refused before anything is written: a session is its instruction,
	// and one without it would leave a run directory with nothing to do.
	if strings.TrimSpace(o.Instruction) == "" {
		return Outcome{}, fmt.Errorf("run: a session needs an instruction")
	}
	if key == "" && o.NoBundle {
		key = SessionKey(o.Instruction, r.now())
	}
	return r.runOne(ctx, key, store.KindSession, o, nil, newPool(r.onPause))
}

// answerFile is where a reply-first run keeps its reply, beside the note a
// triage or rca run also files.
const answerFile = "answer.md"

// completeSession writes the reply to answer.md in the run directory.
func (r *Runner) completeSession(p *prepared, reply string) error {
	path := filepath.Join(p.run.Dir, answerFile)
	if err := os.WriteFile(path, []byte(strings.TrimRight(reply, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("run: write %s: %w", answerFile, err)
	}
	return nil
}

// readBundleIfAny is readBundle for a run that may have none: a missing ticket.json is an
// empty bundle, not an error.
func readBundleIfAny(dir string) (ticket.Bundle, error) {
	if _, err := os.Stat(filepath.Join(dir, "ticket.json")); errors.Is(err, fs.ErrNotExist) {
		return ticket.Bundle{}, nil
	}
	return readBundle(dir)
}
