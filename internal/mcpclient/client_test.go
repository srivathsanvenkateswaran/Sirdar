package mcpclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStartHandshake(t *testing.T) {
	c, stderr := startFake(t, "1")

	info := c.ServerInfo()
	if info.Name != "fake-mcp" || info.Version != "9.9.9" || info.ProtocolVersion != "2025-06-18" {
		t.Fatalf("ServerInfo = %+v", info)
	}
	if !waitFor(t, 5*time.Second, func() bool {
		return strings.Contains(stderr.String(), "listening on stdio")
	}) {
		t.Fatalf("server stderr not captured: %q", stderr.String())
	}
}

func TestListToolsPaginates(t *testing.T) {
	c, _ := startFake(t, "1")

	tools, err := c.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	if got := strings.Join(names, ","); got != "echo,fail,slow" {
		t.Fatalf("tools = %q, want the two pages concatenated", got)
	}
	if tools[0].Description != "echo a message" {
		t.Fatalf("description = %q", tools[0].Description)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("inputSchema = %s (%v)", tools[0].InputSchema, err)
	}
}

// TestCallToolText also covers the roots/list answer: the fake asks for
// the roots before its first tool call and echoes back what it got.
func TestCallToolText(t *testing.T) {
	c, _ := startFake(t, "1")

	out, isErr, err := c.CallTool(t.Context(), "echo", json.RawMessage(`{"message":"hi"}`))
	if err != nil || isErr {
		t.Fatalf("out=%q isErr=%v err=%v", out, isErr, err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("content items not flattened one per line: %q", out)
	}
	if lines[1] != "echo: hi" {
		t.Fatalf("text item = %q", lines[1])
	}
	if lines[2] != "[image image/png]" {
		t.Fatalf("image item = %q", lines[2])
	}
	if !strings.HasPrefix(lines[0], "roots: file://") || !strings.Contains(lines[0], "(workspace)") {
		t.Fatalf("roots/list was not answered with the workspace root: %q", lines[0])
	}
}

func TestCallToolIsError(t *testing.T) {
	c, _ := startFake(t, "1")

	out, isErr, err := c.CallTool(t.Context(), "fail", nil)
	if err != nil {
		t.Fatalf("a tool that fails on its own terms is not a client error: %v", err)
	}
	if !isErr || !strings.Contains(out, "boom: the tool failed") {
		t.Fatalf("out=%q isErr=%v", out, isErr)
	}
}

func TestCallToolProtocolError(t *testing.T) {
	c, _ := startFake(t, "1")

	if _, _, err := c.CallTool(t.Context(), "nope", nil); err == nil {
		t.Fatal("want an error for a JSON-RPC error response")
	} else if !strings.Contains(err.Error(), "unknown tool: nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallToolTimeout(t *testing.T) {
	c, _ := startFake(t, "1")

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := c.CallTool(ctx, "slow", nil)
	if err == nil {
		t.Fatal("want a timeout")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v; the caller's deadline should win", elapsed)
	}

	// The client stays usable: the abandoned response is dropped, not
	// mistaken for the next call's.
	out, _, err := c.CallTool(t.Context(), "echo", json.RawMessage(`{"message":"after"}`))
	if err != nil || !strings.Contains(out, "echo: after") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// TestServerRequestsAndNotifications checks the reverse direction: an
// unknown notification is ignored rather than fatal, and a server
// request Sirdar does not implement is refused with -32601 instead of
// being left unanswered.
func TestServerRequestsAndNotifications(t *testing.T) {
	c, stderr := startFake(t, "1")

	if _, err := c.ListTools(t.Context()); err != nil {
		t.Fatalf("an unknown notification broke the session: %v", err)
	}
	if !waitFor(t, 5*time.Second, func() bool {
		return containsAll(stderr.String(), "sampling rejected: -32601")
	}) {
		t.Fatalf("sampling/createMessage was not refused: %q", stderr.String())
	}
}

func TestCloseKillsHungServer(t *testing.T) {
	c, _ := startFake(t, "hang")

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- c.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 6*time.Second {
			t.Fatalf("Close took %v", elapsed)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Close did not return within 6s")
	}
}

func TestStartRejectsEmptyCommand(t *testing.T) {
	if _, err := Start(t.Context(), ServerConfig{Name: "broken"}, nil); err == nil {
		t.Fatal("want an error")
	}
}
