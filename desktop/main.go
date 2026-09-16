// Command sirdar-desktop is the Wails shell for Sirdar. It renders the same
// frontend the CLI serves from `sirdar serve`, but talks to the core in-process
// through a bound Go struct instead of HTTP.
package main

import (
	"context"
	"embed"
	"fmt"
	"os"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// appIcon is the same PNG Wails cuts the macOS .icns and the Windows .ico
// from. Those two platforms read it off disk at build time; GTK wants the
// bytes at run time, for the window's own icon — what a Linux taskbar,
// alt-tab switcher and "minimised window" state draw. Without it the
// window falls back to the desktop's generic application glyph.
//
//go:embed build/appicon.png
var appIcon []byte

// programName is what GTK's g_set_prgname is set to, which is where the
// window's WM_CLASS comes from. It has to match the StartupWMClass of
// build/linux/sirdar.desktop or the running window does not group under
// the launcher icon that started it, and the taskbar draws the generic
// glyph next to a correctly-iconified launcher. Lower case, never
// localised: it is an identifier, not the display name, which is "Sirdar"
// and is set separately below.
const programName = "sirdar"

// version is stamped at release time with
// `wails build -ldflags "-X main.version=..."`, mirroring cmd/sirdar/main.go.
// Unstamped desktop dev builds keep this default.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sirdar-desktop:", err)
		os.Exit(1)
	}
}

func run() error {
	path, err := app.DefaultRegistryPath()
	if err != nil {
		return err
	}
	svc := app.New(&app.Registry{Path: path}, app.BuildDeps, app.Options{Stderr: os.Stderr})

	return wails.Run(&options.App{
		Title:     "Sirdar",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// The webview draws on the GPU. On macOS WKWebView always does and Wails
		// offers no switch for it; on Linux, WebKitGTK's hardware acceleration
		// is off by default when no Linux options are given, and every scroll
		// and fade is then rasterised on the CPU. Not frameless: the native
		// title bar costs nothing here and keeps the window's own compositing.
		Linux: &linux.Options{
			WebviewGpuPolicy: linux.WebviewGpuPolicyAlways,
			Icon:             appIcon,
			ProgramName:      programName,
		},
		OnStartup: func(ctx context.Context) {
			svc.Start(ctx)
			go forward(ctx, svc)
		},
		OnShutdown: func(context.Context) {
			svc.Stop()
		},
		Bind: []interface{}{
			NewBridge(svc),
		},
	})
}

// forward relays the service's fan-out onto the Wails runtime, emitting one
// runtime event per app event under its own kind, so the frontend listens
// with EventsOn("run.updated") and friends. The payload is the same JSON the
// SSE stream sends, kind field included.
func forward(ctx context.Context, svc *app.Service) {
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			wailsruntime.EventsEmit(ctx, e.Kind, e)
		}
	}
}
