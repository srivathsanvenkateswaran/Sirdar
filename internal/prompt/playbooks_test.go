package prompt

import "testing"

func TestTitleAndLedeIgnoresByteOrderMark(t *testing.T) {
	title, lede := TitleAndLede("\uFEFF# Deploys\n\nFreeze on Fridays.\n")
	if title != "Deploys" || lede != "Freeze on Fridays." {
		t.Fatalf("TitleAndLede = %q, %q; want %q, %q", title, lede, "Deploys", "Freeze on Fridays.")
	}
}
