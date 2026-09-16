# Qwen Code

`provider: qwen` drives Qwen Code, a Gemini CLI fork whose headless mode is modelled on Claude
Code's. Despite the name it talks to any OpenAI-compatible endpoint, and it is the one
third-party runtime that meets the whole of Sirdar's contract natively: streaming events,
schema-constrained output, resume, MCP, and a per-call permission decision the host makes.

| | |
|---|---|
| Config value | `provider: qwen` |
| Binary | `qwen` (`npm i -g @qwen-code/qwen-code`), looked up on `PATH`, or the path in `qwen.path` |
| Endpoint | With no `qwen:` block, whatever login the CLI already has. `qwen.baseUrl`, `qwen.model` and `qwen.apiKey` together select an OpenAI-compatible backend; all three are needed, even for a local server that checks no key |
| Credential | `qwen.apiKey` is a [credential reference](../../credentials.md), resolved once at startup and passed to the child as `OPENAI_API_KEY` only |
| Read-only mechanism | A loopback HTTP listener registered as a `PreToolUse` hook through a private system settings file (`QWEN_CODE_SYSTEM_SETTINGS_PATH`). Every tool call is posted to it and judged by the same `permissions.bash` and `permissions.mcp` rules as every other provider |
| Environment | Everything Qwen Code reads to choose a backend, credential, settings location or system prompt is stripped from the child's environment before the configured values go back in, so a session runs what the workspace configured, not what the launching shell exported |

## In the configuration reference

- [`provider: qwen`](../../config.md#provider-qwen): the `qwen:` block, the hook, the
  folder-trust posture, and the settings layers.
- [MCP access](../../config.md#mcp-access)
- [Budgets](../../config.md#budgets)

## Related

- [Research: Qwen Code wire formats](../../research/09-qwen-wire-formats.md)
- [Research: multi-model runtimes](../../research/providers/multi-model-runtimes.md)
