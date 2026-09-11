package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/httpapi"
	"github.com/srivathsanvenkateswaran/sirdar/internal/httpapi/ui"
)

func init() { commands["serve"] = cmdServe }

// serveShutdownTimeout is how long an interrupted server waits for the
// requests in flight before it drops them.
const serveShutdownTimeout = 5 * time.Second

// cmdServe runs the desktop UI in the browser: the same frontend the Wails
// app embeds, served from this binary over loopback.
func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr,
		"usage: sirdar serve [--addr 127.0.0.1:7777] [--open] [--workspace PATH] [--allow-remote]")
	addr := fs.String("addr", "127.0.0.1:7777", "address to listen on")
	openBrowser := fs.Bool("open", false, "open the UI in the default browser")
	workspace := fs.String("workspace", "", "workspace to register (default: the one the working directory is in)")
	allowRemote := fs.Bool("allow-remote", false, "permit a non-loopback address (there is no authentication)")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	// The API starts runs and reads notes with the operator's own agent
	// login, so an address anyone can reach is a decision, not a default.
	if !isLoopback(*addr) {
		if !*allowRemote {
			fmt.Fprintf(stderr, "sirdar serve: %s is not a loopback address; pass --allow-remote to bind it anyway\n", *addr)
			return exitUsage
		}
		fmt.Fprintf(stderr, "sirdar serve: warning: %s is reachable from other machines and there is no authentication;"+
			" anyone who can reach it can start runs and read your notes\n", *addr)
	}

	root, ok := serveRoot(*workspace, stderr)
	if !ok {
		return exitUsage
	}

	registryPath, err := app.DefaultRegistryPath()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	reg := &app.Registry{Path: registryPath}
	wsID, err := ensureRegistered(reg, root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	hooks, ok := serveHooks(root, wsID, *allowRemote, stderr)
	if !ok {
		return 1
	}

	ctx, stop := interruptible()
	defer stop()

	svc := app.New(reg, app.BuildDeps, app.Options{Stderr: app.Synced(stderr)})
	svc.Start(ctx)
	defer svc.Stop()

	return serveHTTP(ctx, httpapi.New(svc, ui.FS(), hooks...), *addr, *openBrowser, stdout, stderr)
}

// serveHooks builds the workspace's webhook receiver, if it configured
// one, and says on stderr what was turned on. A workspace with
// webhooks.enabled false gets no options and no output: the routes are
// registered either way and answer 404 without a receiver.
//
// The receiver holds one workspace's secrets, so it is served under one
// workspace id — wsID, the workspace this command was started in. A
// delivery that names another registered workspace in its path gets a 404
// rather than a run in a workspace whose secret it does not hold.
//
// A secret that cannot be resolved stops the command rather than starting
// a server whose endpoints reject every delivery.
func serveHooks(root, wsID string, allowRemote bool, stderr io.Writer) ([]httpapi.Option, bool) {
	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return nil, false
	}
	rc, err := app.BuildReceiver(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar serve: %v\n", err)
		return nil, false
	}
	if rc == nil {
		return nil, true
	}
	sources := rc.Sources()
	sort.Strings(sources)
	fmt.Fprintf(stderr, "sirdar serve: webhook triggers enabled for %s at POST /hooks/%s/<source>\n",
		strings.Join(sources, ", "), wsID)
	if allowRemote {
		fmt.Fprint(stderr, "sirdar serve: warning: put a TLS reverse proxy in front of this listener."+
			" Several of these sources authenticate with a shared secret in a plain header, which anyone"+
			" watching an unencrypted connection can read and replay\n")
	} else {
		fmt.Fprint(stderr, "sirdar serve: note: the listener is on loopback, so a hosted tracker cannot reach it."+
			" Expose it with --allow-remote behind a TLS reverse proxy, or forward the port through a tunnel\n")
	}
	return []httpapi.Option{httpapi.WithHooks(wsID, rc)}, true
}

// serveRoot resolves which workspace to register: the one named by
// --workspace, or the one the working directory belongs to.
func serveRoot(flagValue string, stderr io.Writer) (string, bool) {
	if flagValue != "" {
		abs, err := filepath.Abs(flagValue)
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return "", false
		}
		if _, err := config.Load(abs); err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return "", false
		}
		return abs, true
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return "", false
	}
	root, err := config.FindRoot(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: no .sirdar/config.yaml found in %s or its parents;"+
			" run 'sirdar init' or pass --workspace PATH\n", cwd)
		return "", false
	}
	return root, true
}

// ensureRegistered adds root to the workspace registry if it is not there
// already, so the UI opens on something rather than an empty board. It
// returns the workspace's id, which is the one the hook routes serve.
func ensureRegistered(reg *app.Registry, root string) (string, error) {
	list, err := reg.List()
	if err != nil {
		return "", err
	}
	want := filepath.Clean(root)
	for _, ws := range list {
		if ws.Root == want {
			return ws.ID, nil
		}
	}
	ws, err := reg.Add(want)
	if err != nil {
		return "", err
	}
	return ws.ID, nil
}

// serveHTTP runs h until ctx is cancelled, then gives the requests in
// flight serveShutdownTimeout to finish.
func serveHTTP(ctx context.Context, h http.Handler, addr string, openBrowser bool, stdout, stderr io.Writer) int {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	// Event streams live as long as their request context. Handing them a
	// context of our own means Ctrl-C ends them at once instead of holding
	// the shutdown open until it times out.
	streams, endStreams := context.WithCancel(context.Background())
	defer endStreams()

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return streams },
	}
	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	url := browseURL(ln.Addr())
	fmt.Fprintf(stdout, "Sirdar UI: %s\n", url)
	if openBrowser {
		if err := openURL(url); err != nil {
			fmt.Fprintf(stderr, "sirdar serve: could not open a browser: %v\n", err)
		}
	}

	select {
	case err := <-serveErr:
		if err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
	}

	endStreams()
	shutdown, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		// A client that will not let go is not a failure of this command:
		// close the rest and leave.
		_ = srv.Close()
	}
	return 0
}

// isLoopback reports whether addr binds only this machine. A host part that
// is empty (":7777") binds every interface, and a name other than localhost
// is not resolved here: it needs --allow-remote either way.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// browseURL turns the bound address into one a browser can open. A listener
// on every interface is advertised as loopback, which is where the operator
// running the command is.
func browseURL(a net.Addr) string {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return "http://" + a.String()
	}
	if tcp.IP == nil || tcp.IP.IsUnspecified() {
		return "http://127.0.0.1:" + strconv.Itoa(tcp.Port)
	}
	return "http://" + net.JoinHostPort(tcp.IP.String(), strconv.Itoa(tcp.Port))
}

// openURL hands the URL to the platform's opener. It does not wait: the
// browser outlives this process.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
