package config

// DefaultConfigYAML is the template written by `sirdar init`. It carries
// explanatory comments and placeholders that init fills in (workspace name)
// or that the operator fills in by hand (adapter command, org ID, keychain
// service).
const DefaultConfigYAML = `workspace: <name>
provider: claude            # claude | codex | openai | acp | qwen
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
# provider: qwen drives the Qwen Code CLI, which speaks the same stream-json
# family as Claude Code and reaches any OpenAI-compatible endpoint. With the
# block left out entirely the session uses the login the qwen CLI already
# has; baseUrl, model and apiKey are named together to point it elsewhere.
# qwen:
#   path: qwen                                # optional: where the CLI lives
#   baseUrl: https://dashscope-intl.aliyuncs.com/compatible-mode/v1
#   model: qwen3-coder-plus
#   apiKey: keychain:dashscope-api-key        # a local server still needs one named
# provider: acp drives any agent that speaks the Agent Client Protocol —
# Gemini CLI, Goose, OpenCode, Qwen Code, Kimi CLI, Crush and about forty
# more — over one adapter. There is no cost signal, and a whole prompt turn
# counts as one turn, so budget.maxMinutes is what bounds an acp run.
# Uncomment the block and set provider: acp to use it.
# acp:
#   command: gemini                         # or goose, opencode, qwen, npx
#   args: ["--experimental-acp"]            # goose: ["acp"]; qwen: ["--acp"]
#   env: {}                                 # added to the agent's environment
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
    # the rest; apiToken, oauthToken, apiKey, clientId, clientSecret and
    # accessToken are credential refs, never literal secrets.
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
    #
    # adapter: helpscout
    # clientId: keychain:helpscout-client-id          # OAuth2 client credentials;
    # clientSecret: keychain:helpscout-client-secret  # Sirdar mints its own tokens
    #
    # adapter: intercom
    # accessToken: keychain:intercom-access-token     # workspace token from Developer Hub
    #
    # adapter: hubspot
    # accessToken: keychain:hubspot-private-app-token # private-app token, pat-na1-...
    #
    # adapter: servicenow
    # instance: acme                          # acme.service-now.com
    # table: incident                         # or sc_task, sn_customerservice_case
    # username: sirdar.integration            # literal login name, not a secret
    # password: keychain:servicenow-password  # basic auth, instead of oauthToken
    # oauthToken: keychain:servicenow-oauth-token  # instead of username + password
    #
    # ServiceNow is the one built-in that serves either role: the same
    # incident is the customer's ticket and the work item, so the same
    # block works under sources.tracker when ServiceNow is where the work
    # is tracked too.
notes:
  dir: .sirdar/notes
  templates: ""              # optional: directory with triage/rca/resolution .md.tmpl overrides
  filenames:
    triage: "{key} {slug}.md"
    rca: "{key} RCA {slug}.md"
    resolution: "{key} RES {slug}.md"
language:
  # The engineer's note is written in notes:. Anything the customer will
  # read — the reply draft on a triage note, the customer summary on an
  # RCA — is written in customer:, and "auto" means the language of the
  # ticket's first customer message. The verbatim original complaint is
  # kept either way.
  notes: en
  customer: auto
  # Wrap a right-to-left paragraph in <div dir="rtl"> so Obsidian lays it
  # out the way the customer wrote it. Applies to the built-in templates
  # only; with notes.templates set, your templates own their markup.
  rtlMarkup: true
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
  # Hosts a session may fetch a URL from — Claude's WebFetch, the openai
  # loop's and qwen's web_fetch, an ACP fetch request. While this list is
  # empty nothing is fetchable, on any provider, and each refusal names the
  # host it turned down. "docs.example.com" is that host exactly;
  # "*.example.com" is its subdomains and not the bare domain; a service on
  # this machine is named as "http://localhost:3000", which is the only way
  # http and the only way a loopback address gets through.
  #
  # Keep it to hosts a triage genuinely reads. A fetch the session chooses
  # the destination of is how text a read tool pulled in — a ticket
  # comment, a page, a file — gets to send what the run knows somewhere.
  fetch: []
    # - "docs.microsoft.com"
    # - "*.readthedocs.io"
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

# Inbound triggers: a tracker or helpdesk POSTs to "sirdar serve" when a
# ticket lands on you, and Sirdar triages it without being asked. Off until
# enabled, and the endpoints exist only while "sirdar serve" is running.
#
# The URL one source posts to is
#   POST http://<host>:<port>/hooks/<workspace-id>/<source>
# and "sirdar serve" prints the workspace id at startup.
#
# "serve" binds loopback, which a hosted tracker cannot reach: exposing the
# endpoints means "serve --allow-remote" behind a TLS reverse proxy, or a
# tunnel. Five of these sources authenticate with a shared secret in a plain
# header, which anyone watching an unencrypted connection can read and
# replay. docs/webhooks.md has the per-source setup steps.
#
# webhooks:
#   enabled: true
#   cooldown: 10m            # a key triaged this recently is skipped; 0s disables
#   match:
#     assignee: me           # "me" is the account email on sources.tracker/helpdesk
#     statuses: [Open, "In Progress"]   # optional; matched against whatever the payload carries
#     labels: [support]                 # optional; same
#   sources:                 # every secret is a credential ref, never the secret itself
#     jira:
#       secret: keychain:jira-hook-secret        # X-Sirdar-Secret, set on the Automation rule
#     linear:
#       secret: keychain:linear-hook-secret      # Linear's signing secret (Linear-Signature)
#     azdo:
#       username: sirdar                         # basic auth on the service hook subscription
#       password: keychain:azdo-hook-password
#     rally:
#       secret: keychain:rally-hook-secret       # X-Sirdar-Secret
#     zendesk:
#       secret: keychain:zendesk-hook-secret     # the webhook signing secret
#     freshdesk:
#       secret: keychain:freshdesk-hook-secret   # X-Sirdar-Secret
#     intercom:
#       secret: keychain:intercom-client-secret  # the app client secret (X-Hub-Signature)
#     hubspot:
#       secret: keychain:hubspot-client-secret   # the private app client secret (v3 signature)
#     generic:
#       secret: keychain:sirdar-hook-secret      # X-Sirdar-Secret; body {"key":"...","assignee":"..."}
`
