package main

import "fmt"

// OpenNote opens one of a run's notes in whatever the desktop associates
// with a Markdown file — on a machine with a vault, Obsidian. The path has
// to be one the run itself recorded in its state, which is the whole guard:
// the frontend can name only a file a run of a registered workspace wrote,
// never an arbitrary one.
//
// Like OpenConfig it has no app.Service counterpart and is not in
// bridgeMethods: a browser served by `sirdar serve` cannot open a file on
// the operator's machine, and the frontend copies the path there instead.
func (b *Bridge) OpenNote(ws, runID, path string) error {
	detail, err := b.svc.Run(ws, runID)
	if err != nil {
		return err
	}
	for _, note := range detail.Notes {
		if note == path {
			return openWithDesktop(path)
		}
	}
	return fmt.Errorf("run %s recorded no note at %q", runID, path)
}
