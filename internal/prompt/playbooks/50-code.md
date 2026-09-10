The workspace codebase is checked out read-only so you can confirm a hypothesis against what
the code actually does, not just what the ticket or the logs suggest it does.

## How to use it

- Cite every code claim as file:line; a description without a location can't be checked.
- Follow the call path from the UI action through the endpoint it hits to the data it reads or
  writes, rather than stopping at the first function that looks relevant.
- Check sibling code paths (other branches, other callers, other tenants' configuration) for
  the same bug before concluding it's isolated to this one case.
- Read the surrounding tests, if any, to see what behaviour was already considered and
  intentional versus what looks like an oversight.

## Workspace gotchas

<!-- add the mistakes this workspace has already made once -->
