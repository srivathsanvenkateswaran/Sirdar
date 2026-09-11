package httpx

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxNameBytes is the ceiling SanitizeName caps a filename component at.
// It leaves room for the "<index>-" prefix the adapters add and stays well
// inside the 255-byte component limit every filesystem in play enforces.
const MaxNameBytes = 120

// FallbackName is what a name that sanitises to nothing usable becomes.
const FallbackName = "attachment"

// SanitizeName turns a filename taken from an API response into a safe path
// component: any directory portion is dropped (so "../../evil.txt" cannot
// write outside the destination dir), backslashes are treated as separators
// too (a Windows-shaped name is input like any other), path separators and
// control characters are stripped, an empty/"."/".." result becomes
// "attachment", and the result is capped at MaxNameBytes without splitting
// a rune and preserving the extension.
func SanitizeName(name string) string {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))

	var b strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		clean = FallbackName
	}
	return capBytes(clean, MaxNameBytes)
}

// capBytes truncates name to at most max bytes, preserving its extension
// where possible and never splitting a multi-byte UTF-8 rune.
func capBytes(name string, max int) string {
	if len(name) <= max {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) >= max {
		return truncateValidUTF8(name, max)
	}
	return truncateValidUTF8(name[:len(name)-len(ext)], max-len(ext)) + ext
}

func truncateValidUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
