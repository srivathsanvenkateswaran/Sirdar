# Webhook fixtures, and where their shapes come from

One realistic delivery per source, trimmed to the fields the extractor reads plus enough
surrounding structure to keep the shape honest. Names, ids, addresses and timestamps are
invented.

| File | Shape | Provenance |
|---|---|---|
| `jira-issue-updated.json` | Jira Cloud `jira:issue_updated` | Documented: the platform webhook envelope (`webhookEvent`, `issue`, `changelog`). Assignment has no event of its own — it arrives as an update with an `assignee` changelog item (`docs/research/adapters/jira.md` §6). |
| `jira-automation.json` | Jira Automation "send web request" | **Convention, not a schema.** An Automation rule sends whatever body the operator writes; this is the flat shape the setup in `docs/webhooks.md` tells them to write. |
| `linear-issue-update.json` | Linear `Issue` `update` | Documented: `action`, `type`, `data`, `updatedFrom`, `webhookTimestamp`, `webhookId` (`docs/research/adapters/linear.md` §5). `data` mirrors the GraphQL `Issue`, so the field set here is a subset of a real one. |
| `linear-issue-comment-update.json` | Linear `Issue` `update` that changed only the title | Same envelope; exists to prove an update touching neither assignee nor labels produces no trigger. |
| `azdo-workitem-updated.json` | Azure DevOps service hook `workitem.updated` | Envelope documented (`id`, `eventType`, `resource`, `resourceContainers`, …). **The `resource` body is partly unverified**: `docs/research/adapters/azure-devops.md` §5 records that no live "resource details: All" payload for a work-item event was captured, so `resource.fields` (the per-field old/new diff) and `resource.revision.fields` (the post-update snapshot) are reconstructed from Microsoft's field reference and community samples. The extractor reads `resource.workItemId` first for that reason — it is the one field every detail level sends. |
| `rally-defect-updated.json` | Rally webhook delivery | **Unverified.** `docs/research/adapters/rally.md` §5 records that the Webhooks Payload page could not be loaded; `message.state` / `message.changes` / `FormattedID` come from the webhook config object's own vocabulary and from Rally's WSAPI field names. Treat the extractor's Rally paths as a best guess until a real delivery is captured. |
| `zendesk-trigger.json` | Zendesk trigger → webhook | **Operator-authored.** A Zendesk webhook body is a JSON template the trigger fills in with placeholders (`{{ticket.id}}` and friends), so there is no vendor schema — this is the template `docs/webhooks.md` tells the operator to paste. The signing scheme around it *is* documented. |
| `freshdesk-automation.json` | Freshdesk automation → "Trigger webhook" | **Operator-authored**, same reason. |
| `intercom-conversation-assigned.json` | Intercom `conversation.admin.assigned` notification | Documented envelope: `type: notification_event`, `topic`, `data.item`. The `item` is a Conversation; the subset here matches Intercom's conversation model. |
| `hubspot-ticket-events.json` | HubSpot v3 webhook batch | Documented: one JSON array of events, each with `subscriptionType`, `objectId`, `propertyName`, `propertyValue`, `occurredAt`. `hubspot_owner_id` carries a numeric owner id, not an address — which is what `webhooks.match.assignee` has to be set to for HubSpot. |
| `generic.json` | Sirdar's own shape | Defined here, in `docs/webhooks.md`. |
