package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

// exitUsage is the status for a command the operator invoked wrongly: bad
// flags, missing arguments, or no workspace to work in.
const exitUsage = 2

// loadWorkspace finds the workspace the working directory belongs to and
// loads its configuration. The bool is false when the caller should give
// up; the message has already been written to stderr.
func loadWorkspace(stderr io.Writer) (*config.Config, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, false
	}
	root, err := config.FindRoot(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: no .sirdar/config.yaml found in %s or its parents; run 'sirdar init'\n", cwd)
		return nil, false
	}
	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, false
	}
	return cfg, true
}

// interruptible returns a context that Ctrl-C cancels, and the func that
// releases the signal handler. The runner reacts to the cancellation by
// stopping its sessions and marking the runs blocked, so an interrupted
// batch can be picked up again with `sirdar resume`.
//
// The handler is released as soon as the first signal lands, so a second
// Ctrl-C reaches the default handler and kills the process. Otherwise an
// operator who wants out of a shutdown that is taking too long — an agent
// ignoring SIGINT, an adapter inside its five-second grace — would have
// nothing left to press.
func interruptible() (context.Context, func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// buildDeps assembles the runner's dependencies for this workspace. The
// work lives in internal/app so the desktop shell wires a runner the same
// way; the stdout writer is accepted so every command calls it identically,
// though nothing the runner produces goes there.
func buildDeps(cfg *config.Config, providerName, model string, _, stderr io.Writer) (runner.Deps, func(), error) {
	return app.BuildDeps(cfg, providerName, model, stderr)
}
