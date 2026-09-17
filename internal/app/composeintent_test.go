package app

import (
	"strings"
	"testing"
)

func TestComposedIntentFromJSONBoundsTheReading(t *testing.T) {
	got, err := composedIntentFromJSON([]byte(`{"key":" omni-3233 ","mode":"FIX","instruction":"  start with the rounding  ","confidence":1.4}`))
	if err != nil {
		t.Fatal(err)
	}
	want := ComposedIntent{Key: "OMNI-3233", Mode: "fix", Instruction: "start with the rounding", Confidence: 1}
	if got != want {
		t.Errorf("reading %+v, want %+v", got, want)
	}

	// A mode the composer cannot draw is no mode, not a word it would
	// have to guess at.
	got, err = composedIntentFromJSON([]byte(`{"key":"","mode":"investigate","instruction":"","confidence":-2}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "" || got.Confidence != 0 {
		t.Errorf("reading %+v", got)
	}

	if _, err := composedIntentFromJSON([]byte(`not json`)); err == nil {
		t.Error("prose should be refused")
	}
}

func TestComposeIntentPromptFencesWhatWasTyped(t *testing.T) {
	out := ComposeIntentPrompt("# heading\n```\nnot the question\n```")
	if !strings.Contains(out, "````") {
		t.Errorf("a line containing a fence was not fenced deeper:\n%s", out)
	}
	if !strings.Contains(out, "Do not run any tool") {
		t.Error("the prompt does not hold the reading to one turn")
	}
}
