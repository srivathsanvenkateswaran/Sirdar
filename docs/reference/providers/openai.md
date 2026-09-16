# OpenAI-compatible endpoints

`provider: openai` spawns no CLI. Sirdar runs the agent loop itself against any
OpenAI-compatible Chat Completions endpoint: an aggregator, a vendor, or a server on your own
machine.

| | |
|---|---|
| Config value | `provider: openai` |
| Endpoint | `openai.baseUrl`, for example `https://openrouter.ai/api/v1` or `http://localhost:11434/v1` |
| Credential | `openai.apiKey`, a [credential reference](../../credentials.md); omit it for a local server that needs none |
| Model | `openai.model`, plus `openai.maxContextTokens`, `openai.price` (so `budget.maxUsd` has a number to work with), `openai.temperature` and `openai.extraHeaders` |
| Read-only mechanism | The loop's tool set, `internal/agenttools`, has no write tool to withhold; shell and MCP calls go through the same `PermissionPolicy` as every other provider, decided before each call |
| Where it runs | Aggregators (OpenRouter, Groq, Together, Fireworks), vendors (DeepSeek, Zhipu, Moonshot, DashScope, xAI), or local servers (Ollama, vLLM, LM Studio, llama.cpp) |

## In the configuration reference

- [`provider: openai`](../../config.md#provider-openai): the `openai:` block, context sizing and
  pricing.
- [MCP access](../../config.md#mcp-access)
- [Budgets](../../config.md#budgets)

## Related

- [Spec: openai provider design](../../superpowers/specs/2026-09-10-openai-provider-design.md)
- [Architecture: the three read-only layers](../../architecture.md#the-three-read-only-layers)
- [Research: multi-model runtimes](../../research/providers/multi-model-runtimes.md)
