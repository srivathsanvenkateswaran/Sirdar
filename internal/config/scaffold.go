package config

// DefaultConfigYAML is the template written by `sirdar init`. It carries
// explanatory comments and placeholders that init fills in (workspace name)
// or that the operator fills in by hand (adapter command, org ID, keychain
// service).
const DefaultConfigYAML = `workspace: <name>
provider: claude            # claude | codex
model: ""                   # provider default when empty
billing: subscription       # subscription | api (api keeps ANTHROPIC_API_KEY in the agent's environment)
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
