package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenConfig opens the workspace's .sirdar/config.yaml in whatever the
// desktop associates with a YAML file. The app reads that file and never
// writes it, so every Settings row whose value lives there offers this
// instead of an editor.
//
// It has no app.Service counterpart, which is why it is not in
// bridgeMethods: a browser served by `sirdar serve` cannot open a file on
// the operator's machine, and the frontend copies the path there instead.
// The workspace is named by id rather than by path so the only file this
// can open is a registered workspace's own configuration.
func (b *Bridge) OpenConfig(ws string) error {
	workspaces, err := b.svc.Workspaces()
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		if w.ID != ws {
			continue
		}
		return openWithDesktop(filepath.Join(w.Root, ".sirdar", "config.yaml"))
	}
	return fmt.Errorf("workspace %q is not registered", ws)
}

// openWithDesktop hands a path to the operating system's opener. A test
// swaps it for a recorder, since the real one launches an editor.
var openWithDesktop = func(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
