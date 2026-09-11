## What this changes

<!-- One or two sentences: what changed and why. -->

## Checklist

- [ ] Tests added or updated (`make test` passes)
- [ ] `docs/config.md` updated if this adds, renames, or changes the default of a config key
- [ ] No credentials in test fixtures — `httptest` fixtures use fake tokens, not real ones
- [ ] Any new credentialed fetch (attachment download, pagination link, redirect) is host-checked
      before the credential is sent
- [ ] No AI attribution in commit messages or this description
