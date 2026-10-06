package mcpclient

import (
	"context"
	"encoding/json"
	"time"
)

// ConnectTimeout bounds starting a server and its initialize handshake. A
// server that has not answered by then is reported as an error rather than
// left holding the caller open.
const ConnectTimeout = 30 * time.Second

// CallOnce starts one server, calls one tool on it and closes it again.
// It is the whole of a hand-run `sirdar mcp call` and of a gather step's
// MCP call, so the two cannot drift apart on how a server is reached.
//
// The handshake is bounded by ConnectTimeout as well as by ctx; the call
// itself only by ctx. Whatever the server says back has already had the
// entry's credentials taken out of it by Connect's wrapper. isErr is the
// server's own flag — the tool ran and reported a failure — which is not
// the same as err, a call that did not happen.
func CallOnce(ctx context.Context, e Entry, tool string, args json.RawMessage) (text string, isErr bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	conn, err := Connect(cctx, e, nil)
	cancel()
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	return conn.CallTool(ctx, tool, args)
}
