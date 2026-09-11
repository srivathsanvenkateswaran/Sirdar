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
    # Built-in trackers, as alternatives to an exec adapter. Pick one and
    # delete the rest; apiToken, pat and apiKey are credential refs, never
    # literal secrets.
    #
    # adapter: jira
    # baseUrl: https://acme.atlassian.net   # or the Data Center instance URL
    # deployment: auto                      # cloud | datacenter | auto (probes /rest/api/2/serverInfo)
    # email: you@acme.com                   # Cloud: sent with apiToken as basic auth
    # apiToken: keychain:jira-api-token     # Cloud
    # pat: keychain:jira-pat                # Data Center, instead of email + apiToken
    # projectKey: ACME                      # optional: scopes list to one project
    # epicLinkField: customfield_10014      # optional: Data Center epic-link field, by id or name
    #
    # adapter: linear
    # apiKey: keychain:linear-api-key
    # teamKey: ENG                          # optional: scopes list to one team
    #
    # adapter: azdo
    # orgUrl: https://dev.azure.com/acme    # or a Server collection URL
    # project: Payments
    # pat: keychain:azdo-pat
    # helpdeskLinkDomain: acme.zohodesk.com # optional: host marking a Hyperlink relation as the helpdesk ticket
    # helpdeskField: Custom.SupportTicket   # optional: fallback custom field
    #
    # adapter: rally
    # baseUrl: https://rally1.rallydev.com  # default
    # apiKey: keychain:rally-api-key
    # workspace: "12345678910"              # workspace _ref or ObjectID
    # project: "12345678911"                # optional: scopes list
    # types: [Defect, HierarchicalRequirement]
    # helpdeskField: c_SupportTicket        # optional
    #
    # Fallback for a tracker that records the helpdesk link only as text in
    # the description. It runs after the adapter's own linkage and fills in
    # only where the adapter found none. Both patterns take exactly one
    # capture group: pattern's group is the reference, and idPattern, when
    # set, narrows it to the id the helpdesk API expects.
    # helpdeskRef:
    #   pattern: 'Zoho Ticket URL:\s*(\S+)'
    #   idPattern: '(\d+)$'
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
    #
    # Built-in helpdesks, as alternatives to zohodesk. Pick one and delete
    # the rest; apiToken, oauthToken and apiKey are credential refs, never
    # literal secrets.
    #
    # adapter: zendesk
    # subdomain: acme                         # acme.zendesk.com
    # baseUrl: ""                             # optional override, e.g. a proxy
    # email: you@acme.com                     # basic auth, sent with apiToken
    # apiToken: keychain:zendesk-api-token    # basic auth, instead of oauthToken
    # oauthToken: keychain:zendesk-oauth-token  # instead of email + apiToken
    #
    # adapter: freshdesk
    # domain: acme.freshdesk.com
    # apiKey: keychain:freshdesk-api-key
notes:
  dir: .sirdar/notes
  templates: ""              # optional: directory with triage/rca/resolution .md.tmpl overrides
  filenames:
    triage: "{key} {slug}.md"
    rca: "{key} RCA {slug}.md"
    resolution: "{key} RES {slug}.md"
budget:
  # A turn is one model round-trip: one assistant message that calls a tool
  # or gives the final answer. It is the same unit the Claude CLI reports as
  # num_turns, and a triage of a busy ticket takes 40-60 of them.
  maxTurns: 120
  maxMinutes: 25
  # Claude reports cost only when the session ends, so this is an end-of-run
  # check: it records an overspend, it cannot stop one. maxTurns and
  # maxMinutes are the budgets that bite while a run is going.
  maxUsd: 5
concurrency: 1
attachments:
  maxBytes: 10485760       # 10 MiB. Anything larger, and anything the session
                           # cannot open (audio, video), is dropped from the
                           # bundle and named in a warning instead.
permissions:
  # Every segment of a pipeline or compound command has to match a pattern:
  # "rg foo | head -50" needs both "rg *" and "head *". A segment that
  # redirects or substitutes ($(…), backticks, >, >>, <, &>) is refused
  # whatever the patterns say; 2>&1 and 2>/dev/null are the exceptions.
  bash:
    - "git log*"
    - "git show*"
    - "git grep*"
    - "rg *"
    - "ls *"
    - "cat *"
    - "head *"
    - "tail *"
    - "wc *"
    - "file *"
    - "which *"
    - "echo *"
  # Globs matched against an MCP tool's full name. While this list is empty,
  # a tool is allowed unless a word of its name is a write verb (create,
  # update, delete, send, deploy, buy, save, log, …). A name that also
  # carries a read word (query, select, read, search, list, get, find,
  # describe, show) is a read whatever else it says, so run_query and
  # run_select go through. Naming patterns here replaces that heuristic
  # outright: anything unlisted is then denied.
  mcp: []
    # - "mcp__grafana__query_*"
    # - "mcp__grafana__list_*"
mcp:
  # Start the session against <workspace>/.mcp.json and nothing else, so the
  # operator's own global connectors are not loaded into a triage run. With
  # no such file the session is started against an empty MCP config and has
  # no MCP tools at all, so a playbook that names one gets nothing; write the
  # servers the playbooks need into <workspace>/.mcp.json. doctor says which
  # of the three you are in.
  workspaceOnly: true
playbooks: .sirdar/playbooks
# Post a short digest to a chat channel or a webhook when a run finishes.
# Metadata and note paths only: no note text ever leaves the machine, and
# the ticket title is sent only with includeTitle: true, because a support
# subject line routinely names the customer. A failed post is a warning on
# the run, never a failed run. SIRDAR_NO_NOTIFY=1 silences one invocation.
# notify:
#   on: [completed, failed, over_budget, blocked]   # default: all four
#   includeTitle: false
#   slack:
#     webhookUrl: keychain:sirdar-slack-webhook     # a credential ref: the URL is the credential
#   teams:
#     webhookUrl: keychain:sirdar-teams-webhook     # Workflows (Power Automate) or connector URL
#   generic:
#     - url: https://hooks.example.com/sirdar       # https, or http on loopback
#       headers:
#         Authorization: env:SIRDAR_HOOK_TOKEN      # env:/keychain: values are resolved
#       secret: env:SIRDAR_HOOK_SECRET              # signs the body as X-Sirdar-Signature
`
