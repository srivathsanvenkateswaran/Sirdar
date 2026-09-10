# Landscape: coding-agent orchestrators for L2/L3 support tickets (checked 2026-09-10)

Target workflow used for scoring: helpdesk or tracker ticket in, bring-your-own-subscription
coding agent, evidence via MCP (DB, logs, APM, Slack), Arabic to English translation,
root-cause note, optional fix PR, kanban or dashboard. Score 5 = all of it, 1 = one piece.

**Short answer: nothing does the whole loop.** Two SaaS products come closest (Devin
Auto-Triage, Linear Agent). The open-source pieces exist separately: Symphony's spec for
tracker-driven runs, Cyrus for a BYO-CLI runner on a webhook, Paperclip and Multica for the
board, HolmesGPT for evidence gathering. No project found ingests a *helpdesk* ticket (Zoho
Desk, Zendesk, Freshdesk, Intercom) directly into a coding agent, and none translates.

## Orchestrators, harnesses, coding agents

| Name | Type / license | BYO subscription? | Ticket source | Evidence (MCP?) | Output | UI | Score |
|---|---|---|---|---|---|---|---|
| OpenAI Symphony | OSS Apache-2.0 (spec + Elixir reference) | Runs local `codex app-server`, so whatever Codex CLI is logged in with (not stated explicitly) | Linear, GitHub Issues, Jira Cloud, Asana, GitLab adapters; spec is tracker-agnostic | Whatever Codex's MCP config provides; MCP elicitation marks issue blocked | PR; some issues "never touch the codebase" | Optional Phoenix LiveView dashboard; rich UI is a stated non-goal | 3 |
| Paperclip | OSS MIT | Yes, `claude_local` adapter uses terminal OAuth login; API key also | Own issue system; BYO ticket system on roadmap | MCP tool gateway shipped | Whatever the agent does | Org chart + tasks + cost dashboard | 3 |
| Multica | OSS, "Multica License" (Apache-2.0 + hosting conditions) | Yes, drives 26 installed signed-in CLIs | Own Linear-shaped issues; Slack/Lark/Telegram triggers; Jira sync is an open request | Not stated | Progress comments, PR | Web kanban, desktop, iOS | 3 |
| Vibe Kanban | OSS Apache-2.0, **sunsetting** (bloop shut down 10 Apr 2026, now local-only community project) | Yes | None | MCP config exists | PR | Kanban | 2 |
| Conductor | Proprietary Mac app, Free / $50 / $60 per user | Yes, "bring your own subscriptions and keys" | Linear (per reviews, unverified) | Not documented | PR | Workspace list, diff review | 2 |
| T3 Code | OSS MIT | Yes, wraps Codex, Claude Code, Cursor, OpenCode, Grok CLIs | None | Not documented | PR | Desktop/web/mobile | 1 |
| Claude Squad | OSS AGPL-3.0 | Yes (tmux + worktrees) | None | No | Branches | TUI | 1 |
| Ralph loop | OSS | Yes | PRD file | No | Commits | None | 1 |
| Cyrus | OSS Apache-2.0 (+ hosted) | "BYOK: bring your keys / subscriptions" | Linear, GitHub, GitLab, Slack: assign issue, worktree, agent | Not stated | Streams activity back with approvals; PRs | Hosted dashboard or self-host | 3 |
| Untrivial agent-orchestrator | OSS Apache-2.0 | Per harness (27 incl. Claude Code, Codex) | Not documented | Not stated | Draft PRs, CI tracking | Live kanban | 2 |
| Open SWE (LangChain) | OSS MIT | API keys | GitHub issues, Slack, Linear, schedules | Not stated | PR | Web dashboard | 3 |
| Kilo Cloud Agents | SaaS | Unverified | Webhook from any system | Not stated | PR | app.kilo.ai | 2 |
| Kodus | OSS AGPL-3.0 | BYO model | PR review only | n/a | Review comments | Web | 1 |
| Plane | OSS AGPL-3.0 + commercial | BYO OpenAI/Anthropic keys | Own work items | Plane MCP server (as a source) | Not documented | Kanban | 2 |
| OpenHands | OSS MIT + Cloud | BYO LLM key | GitHub, GitLab, Bitbucket, Slack; Jira DC enterprise | Not stated | PR | Web | 2 |
| SWE-agent | OSS MIT | API key | GitHub issue | No | Patch | CLI | 1 |
| Devin (Cognition) | SaaS, ACU-metered | No | Auto-Triage (18 May 2026): Slack, Linear, GitHub, Sentry, Datadog, escalations, webhooks | "inspect the codebase, check observability tools, look through related tickets" | Investigation summary, owner tagging, dedup, PR if clear | Devin web + Linear/Slack | **4** |
| Factory Droids | SaaS, BYOK API keys only | Partial | Linear, Jira, Slack; reads Sentry | MCP | PR | Web/CLI | 3 |
| Cursor Automations | SaaS | No | Slack, Linear, GitHub, PagerDuty, Sentry, webhooks, schedules | Configured MCPs | PR | Cursor web | 3 |
| GitHub Copilot cloud agent | SaaS | No | GitHub issues; Jira (GA Jun 2026); Linear (GA Jul 2026) | GitHub + Playwright MCP; Atlassian MCP | Draft PR | GitHub | 3 |
| Claude Code routines | Anthropic cloud, research preview, **subscription only** | Yes | Schedule, `/fire` API, GitHub events. Docs' own example is "triages support requests" | claude.ai connectors or committed `.mcp.json` | Transcript, draft PR, connector writes | Routines list; no kanban | 3 |
| Claude Tag (Slack) | Team/Enterprise, org-billed | Org-level | Slack @mention, channel watching | Admin connections: Datadog, Sentry, PagerDuty, ticketing, GitHub | RCA in thread, draft PR | Slack thread | 3 |
| Claude Code in Slack (Pro/Max) | Subscription | Yes | Slack @mention to Claude Code on the web | Web-session connectors | One PR per session | claude.ai/code | 2 |
| Claude Code GitHub Action | OSS; `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` | Yes | GitHub only | `--mcp-config` | PR/comments | GitHub | 2 |
| Claude Agent for Jira (Atlassian, beta) | SaaS, Claude Console billing | No | Assign or @mention in Jira | None mentioned | Draft PR | Jira | 2 |
| Jira Automation coding-agent steps | Jira + Rovo | Depends | Jira rule triggers | Agent's own | PR | Jira | 2 |
| Linear Agent | SaaS Business/Enterprise; "uses harnesses like Claude Code or Codex" | No | Zendesk/Intercom conversation to issue; every issue investigated as it comes in | Codebase + git history only | Investigation summary on the issue, then PR | Linear | **4** |
| Tembo | SaaS | Unverified | Sentry, PagerDuty, Linear, Jira, Slack, git events | Not stated | PR with preview | Web | 3 |

## AI SRE / incident tools

| Name | Type | BYO model? | Input | Evidence | Output | Score |
|---|---|---|---|---|---|---|
| Sentry Seer | SaaS, closed, $40/contributor | No | Sentry issue | Traces, breadcrumbs, repo | RCA + Autofix; hands off to Claude Code / Cursor / Copilot with a case file | 3 |
| Grafana Sift / Investigations | Grafana Cloud only; `mcp-grafana` Apache-2.0 | No | Alerts | Metrics, logs, traces | Report | 2 |
| Cleric | SaaS | No | Alerts/Slack | Datadog, Prometheus, Grafana | Diagnosis, read-only | 2 |
| Resolve AI | Enterprise SaaS | No | Incidents | Code, infra, observability | RCA | 2 |
| Datadog Bits AI SRE | SaaS, ~$6.50 per investigation | No | Datadog alerts | Datadog | Investigation | 2 |
| PagerDuty SRE Agent, incident.io AI SRE, Rootly, Parity, Traversal | SaaS | No | Incidents | Vendor data | RCA | 1 to 2 |
| **HolmesGPT** (Robusta, CNCF sandbox) | **OSS Apache-2.0** | **Yes** (OpenAI, Anthropic, Azure, Bedrock, Gemini) | AlertManager, PagerDuty, OpsGenie, Jira; writes findings back | Prometheus, Loki, Grafana, Datadog, PostgreSQL/MySQL, k8s, Sentry, 50+ toolsets | Analysis written back; no PRs | 3 |
| OpenSRE (Tracer) | OSS Apache-2.0 | Yes | PagerDuty, Opsgenie, Jira, Alertmanager, ServiceNow | 60+ tools, MCP + ACP | Slack/PagerDuty/Telegram | 2 |

## Helpdesk-native AI

| Name | Coding-agent handoff? | What it does |
|---|---|---|
| Zoho Desk Zia Agents | No | Support Specialist, Resolution Expert, Sentiment Analyst, Quality Manager. Nothing about engineering handoff or BYO LLM. |
| Zendesk AI agents | No | Agentic procedures, external API calls; Jira as a knowledge source (Aug 2026). |
| Freshdesk Freddy | No | Copilot drafting, summaries, **translation**, auto triage. |
| Intercom Fin | No | Fin Tasks can create Linear/Jira tickets. |
| Pluno | No PR | Attaches a root-cause summary to the Zendesk ticket before a human opens it, pulling Slack, Jira, Sentry, Datadog. Closest helpdesk-side product to the "RCA note first" idea. |
| Duckie | No PR | Log analysis, links commits/PRs, creates bug reports. |
| Brex "AI oncall engineer" | Internal, unreleased | Claude SDK + Datadog/Linear/Glean/Snowflake/codebase, Slack-triggered, structured report with citations, read-only by default, 30 to 45 min reduced to about 3 min. Best published reference architecture for our target. |

## Literal GitHub searches

"support ticket" + "claude code"/"agent sdk" + MCP + dashboard, "zendesk" + "claude agent sdk",
"zoho desk" + claude orchestrator, "L2 support" open-source agent: only MCP connectors (Composio
Zoho Desk/Zendesk, a Zendesk Claude Code skill, Scalekit's Zendesk example), GitHub-issue triage
bots, and generic orchestrators. `openma-ai/open-managed-agents` (Apache-2.0, self-hosted Claude
Managed Agents runtime) is the only adjacent infrastructure piece.

## The gaps

- The helpdesk-to-coding-agent hop does not exist as a product. Orchestrators start at a developer tracker; helpdesk AIs stop at "create a Jira/Linear issue". Linear Agent bridges Zendesk/Intercom to PR but is Linear-hosted, paid tier, code-only evidence, no BYO subscription.
- The evidence layer is split by market. Coding orchestrators treat MCP as a passthrough and never mention DB/APM/log tooling. AI SRE tools (HolmesGPT is the only OSS BYO-key one) never write code.
- RCA note as the primary output is rare (Brex, Pluno, Devin Auto-Triage, Claude Tag). OSS orchestrators are PR-first.
- BYO *subscription* (not API key): Paperclip, Multica, Cyrus, Conductor, Vibe Kanban, routines, GitHub Action. Vendor-billed: Devin, Linear, Copilot, Factory cloud, Claude Agent for Jira, every SRE tool.
- Translation appears in no agentic tool. Only Freddy Copilot has it, on the support-agent side.

The combination (Zoho Desk ticket, own Claude Code/Codex login, New Relic/Grafana/MySQL/Slack
MCPs, Arabic to English, RCA note, optional PR, kanban) is unoccupied. Nearest assembly: Symphony's
tracker-adapter and state-machine spec + a Cyrus-style BYO-CLI runner + HolmesGPT-style evidence
toolsets + a Paperclip/Multica board.

## Unverified

- Symphony: ChatGPT-login vs API-key not stated; inferred from Codex CLI auth docs.
- Conductor's Linear integration: from reviews only.
- Kilo Cloud Agents and Tembo billing.
- MCP support in Multica, Cyrus, Tembo, AO.
- Devin BYO model: no evidence either way.

## Sources

openai.com/index/open-source-codex-orchestration-symphony · github.com/openai/symphony (SPEC.md, elixir/README.md) · developers.openai.com/codex/auth · github.com/paperclipai/paperclip (discussions/1163, issues/498) · github.com/multica-ai/multica (issues/1634) · github.com/BloopAI/vibe-kanban · vibekanban.com/blog/shutdown · conductor.build/pricing · rywalker.com/research/conductor · github.com/pingdotgg/t3code · github.com/smtg-ai/claude-squad · github.com/anthropics/claude-plugins-official/tree/main/plugins/ralph-loop · github.com/cyrusagents/cyrus · github.com/Untrivial-ai/agent-orchestrator · github.com/langchain-ai/open-swe · blog.kilo.ai/p/cloud-agents-the-missing-layer-in · github.com/kodustech/kodus-ai · plane.so/ai · docs.openhands.dev/openhands/usage/settings/integrations-settings · github.com/swe-agent/swe-agent · cognition.com/blog/auto-triage · docs.devin.ai/integrations/jira · docs.factory.ai/cli/byok/overview · cursor.com/docs/cloud-agent/automations · docs.github.com/copilot/concepts/agents/coding-agent/about-coding-agent · github.blog/changelog/2026-06-25-github-copilot-for-jira-is-now-generally-available · github.blog/changelog/2026-07-23-copilot-cloud-agent-for-linear-is-now-generally-available · code.claude.com/docs/en/routines · code.claude.com/docs/en/slack · claude.com/docs/claude-tag/overview · code.claude.com/docs/en/github-actions · support.atlassian.com/jira-software-cloud/docs/set-up-claude-agent-for-jira · community.atlassian.com/forums/Jira-articles/New-Put-your-coding-agents-to-work-with-Jira-Automation/ba-p/3268918 · linear.app/now/code-intelligence-for-linear-agent · linear.app/now/coding-sessions-for-linear-agent · linear.app/changelog/2025-12-11-linear-agent-for-intercom-zendesk-gong · tembo.io · docs.port.io/guides/all/automatically-resolve-tickets-with-coding-agents · docs.sentry.io/product/ai-in-sentry/seer · docs.sentry.io/integrations/coding-agents · grafana.com/whats-new/2026-07-29-assistant-investigations-is-now-generally-available-in-grafana-cloud · github.com/grafana/mcp-grafana · techcrunch.com/2026/02/04/ai-sre-resolve-ai-confirms-125m-raise-unicorn-valuation · support.pagerduty.com/main/docs/sre-agent · incident.io/blog/ai-sre-agent-definition · rootly.com/blog/introducing-the-rootly-mcp-server · github.com/HolmesGPT/holmesgpt · github.com/tracer-cloud/opensre · zoho.com/desk/zia/agents.html · support.zendesk.com/hc/en-us/articles/11190197817754 · linear.app/docs/intercom · pluno.ai · ycombinator.com/companies/duckie · dosu.dev/pricing · brex.com/journal/how-we-built-an-ai-oncall-engineer · composio.dev/toolkits/zoho_desk/framework/claude-code · github.com/openma-ai/open-managed-agents
