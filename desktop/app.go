package main

import "context"

// App is the struct bound to the frontend. D8 replaces it with Bridge, which
// forwards to internal/app.Service and emits Subscribe() events over the Wails
// runtime; until then it carries a single method so the bindings generate.
type App struct {
	ctx context.Context
}

// NewApp returns an App ready to be passed to wails.Run.
func NewApp() *App {
	return &App{}
}

// startup stores the Wails context so runtime calls can be made later.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Ping is a liveness check for the binding layer. D8 removes it.
func (a *App) Ping() string {
	return "pong"
}
