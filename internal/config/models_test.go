package config

import (
	"reflect"
	"testing"
)

func TestPinnedModelsTakesBothSpellingsPerProvider(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+`
providers:
  claude:
    models:
      - claude-opus-4-5-20251101
      - id: " claude-sonnet-4-5 "
        label: " Sonnet 4.5 "
      - ""
  codex:
    models: [gpt-5.6-luna]
  openai:
    models:
      - id: qwen3-coder
        label: Qwen3 Coder
  cursor:
    models: [composer-2.5]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelPin{{ID: "claude-opus-4-5-20251101"}, {ID: "claude-sonnet-4-5", Label: "Sonnet 4.5"}}
	if got := c.PinnedModels("claude"); !reflect.DeepEqual(got, want) {
		t.Fatalf("claude pins = %+v, want %+v", got, want)
	}
	if got := c.PinnedModels("codex"); !reflect.DeepEqual(got, []ModelPin{{ID: "gpt-5.6-luna"}}) {
		t.Fatalf("codex pins = %+v", got)
	}
	if got := c.PinnedModels("openai"); !reflect.DeepEqual(got, []ModelPin{{ID: "qwen3-coder", Label: "Qwen3 Coder"}}) {
		t.Fatalf("openai pins = %+v", got)
	}
	if got := c.PinnedModels("cursor"); len(got) != 1 || got[0].ID != "composer-2.5" {
		t.Fatalf("cursor pins = %+v", got)
	}
	if got := c.PinnedModels("qwen"); got != nil {
		t.Fatalf("qwen pinned nothing, got %+v", got)
	}
	if got := c.PinnedModels("nope"); got != nil {
		t.Fatalf("an unknown provider pins nothing, got %+v", got)
	}
	var nilCfg *Config
	if nilCfg.PinnedModels("claude") != nil {
		t.Fatal("a nil config pins nothing")
	}
}

func TestPinnedModelsRefusesAnUnknownKey(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+`
providers:
  claude:
    models:
      - id: claude-opus-4-5
        lable: typo
`))
	if err == nil {
		t.Fatal("a misspelt key under a pin loaded")
	}
}
