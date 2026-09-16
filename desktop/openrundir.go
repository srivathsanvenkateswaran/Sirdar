package main

import "path/filepath"

// OpenRunDir reveals a run's directory under .sirdar/runs in the desktop's
// file manager. The directory is the one the service itself resolves for
// the run id, so the frontend can name only a run of a registered
// workspace, never a path.
//
// Like OpenNote it has no app.Service counterpart and is not in
// bridgeMethods: a browser served by `sirdar serve` cannot open a folder on
// the operator's machine, and the sidebar's menu leaves the item out there.
func (b *Bridge) OpenRunDir(ws, runID string) error {
	detail, err := b.svc.Run(ws, runID)
	if err != nil {
		return err
	}
	return openWithDesktop(filepath.Dir(detail.BundleDir))
}
