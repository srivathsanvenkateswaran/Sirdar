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
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

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
		Width:     1200,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
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
