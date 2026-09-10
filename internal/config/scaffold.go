package config

// DefaultConfigYAML is the template written by `sirdar init`. It carries
// explanatory comments and placeholders that init fills in (workspace name)
// or that the operator fills in by hand (adapter command, org ID, keychain
// service).
const DefaultConfigYAML = `workspace: <name>
provider: claude            # claude | codex | openai
model: ""                   # provider default when empty
billing: subscription       # subscription | api (api keeps ANTHROPIC_API_KEY in the agent's environment)
# provider: openai runs Sirdar's own agent loop against any OpenAI-compatible
# Chat Completions endpoint: an aggregator, a vendor, or a server on this
# machine. Sirdar owns the read-only tools and connects the workspace's MCP
# servers itself. Uncomment the block and set provider: openai to use it.
# openai:
#   baseUrl: https://openrouter.ai/api/v1     # or http://localhost:11434/v1
#   apiKey: env:OPENROUTER_API_KEY            # omit for a local server that needs none
#   model: qwen/qwen3-coder
#   maxContextTokens: 128000                  # old tool results are trimmed as the prompt nears this
#   price:                                    # optional; cost is reported as 0 without it
#     inputPerMTok: 0.2
#     outputPerMTok: 0.8
#   temperature: 0
#   extraHeaders:
#     HTTP-Referer: https://github.com/srivathsanvenkateswaran/Sirdar
sources:
  tracker:
    adapter: exec
    command: ~/bin/<tracker-adapter>
  helpdesk:
    adapter: zohodesk
    orgId: "<org-id>"
    baseUrl: https://desk.zoho.com
    # Refresh-token grant from a Zoho Self Client. Sirdar mints its own
    # access tokens, so an unattended run keeps working past the hour one
    # of them lives. All three are credential refs, never literal secrets.
    auth:
      clientId: keychain:zoho-desk-client-id
      clientSecret: keychain:zoho-desk-client-secret
      refreshToken: keychain:zoho-desk-refresh-token
      # accountsUrl: https://accounts.zoho.com   # only when baseUrl is not a desk.zoho.{in,com,eu,com.au} host
    # Alternative to auth: one access token you paste in yourself. It stops
    # working an hour after it was issued, so it suits a run you are
    # watching, not a scheduled one.
    # token: keychain:<service>
notes:
  dir: .sirdar/notes
  templates: ""              # optional: directory with triage/rca/resolution .md.tmpl overrides
  filenames:
    triage: "{key} {slug}.md"
    rca: "{key} RCA {slug}.md"
    resolution: "{key} RES {slug}.md"
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
permissions:
  bash:
    - "git log*"
    - "git show*"
    - "git grep*"
    - "rg *"
playbooks: .sirdar/playbooks
`
