package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

// TestServeRefusesRemoteAddress is the guard that matters most here: the
// API has no authentication, so binding anything but loopback has to be
// asked for explicitly.
func TestServeRefusesRemoteAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", ":7777", "192.168.1.20:7777", "[::]:7777"} {
		var out, errb bytes.Buffer
		code := run([]string{"serve", "--addr", addr}, &out, &errb)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (stdout %q)", addr, code, out.String())
			continue
		}
		if !strings.Contains(errb.String(), "--allow-remote") {
			t.Errorf("%s: stderr %q does not mention --allow-remote", addr, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%s: refused invocation still printed %q", addr, out.String())
		}
	}
}

// TestServeWithoutWorkspace covers the other half: a loopback address is
// accepted, and the command then stops on the missing workspace rather
// than binding anything.
func TestServeWithoutWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--addr", "127.0.0.1:0"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), ".sirdar/config.yaml") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestServeRejectsUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--port", "7777"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true,
		"localhost:7777": true,
		"[::1]:7777":     true,
		"127.0.0.1:0":    true,
		"0.0.0.0:7777":   false,
		":7777":          false,
		"[::]:7777":      false,
		"10.0.0.4:7777":  false,
		"sirdar.local:8": false,
		"7777":           false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestBrowseURL(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7777}, "http://127.0.0.1:7777"},
		// A listener on every interface still gets advertised as the one
		// address the operator is certain to be able to open.
		{&net.TCPAddr{IP: net.IPv4zero, Port: 7777}, "http://127.0.0.1:7777"},
		{&net.TCPAddr{IP: net.IPv6loopback, Port: 80}, "http://[::1]:80"},
	} {
		if got := browseURL(tc.addr); got != tc.want {
			t.Errorf("browseURL(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}
