# Credentials

Sirdar needs API tokens to read tickets, and in one case (`provider: openai`) to talk to a
model. It never holds one. Every credential in `.sirdar/config.yaml` is a *reference* — a
string that names where the secret lives — and config load rejects a value that is a secret
instead of a reference.

```yaml
sources:
  helpdesk:
    adapter: zohodesk
    token: keychain:zoho-desk-token     # a reference
    # token: 1000.9f3c…                 # rejected at load
```

Four schemes resolve, all of them on macOS, Linux and Windows:

| Reference | Reads |
|---|---|
| `env:NAME` | the environment variable `NAME` |
| `keychain:SERVICE` | the operating system's own credential store (below) |
| `file:PATH` | a file holding nothing but the secret |
| `cmd:COMMAND` | the standard output of a credential helper you already use |

A reference is resolved when a source is about to be used, held in memory for that run, and
dropped. Which leads to the rule that governs all of this:

**Sirdar never writes a credential anywhere.** Not to a run directory, not to a note, not to a
log line, not to a config file, not to the keychain. It only ever reads. Storing a secret is
something you do once, by hand, with the commands below; Sirdar has no subcommand that will do
it for you, and that is deliberate. A resolved secret goes to exactly one place — the HTTP
client that authenticates the request, or the model endpoint's `Authorization` header — and
never into the environment of the agent session, which can run shell commands.

## `keychain:` — the operating system's store

`keychain:SERVICE` reads a secret the OS keeps encrypted and unlocks with your login. This is
the recommended scheme: nothing is on disk in the clear and nothing is in your shell history.

### macOS — the login keychain

Sirdar reads it with `security find-generic-password -s SERVICE -w`.

```sh
security add-generic-password -s zoho-desk-token -a "$USER" -w
```

`-w` with no value prompts, so the secret stays out of your shell history. The first time
Sirdar reads it, macOS asks whether to let the binary at that path use the keychain; "Always
Allow" remembers the answer.

### Linux and the BSDs — the Secret Service

Sirdar reads the freedesktop Secret Service with libsecret's `secret-tool lookup service
SERVICE`. GNOME Keyring implements it, KWallet implements it behind the
`org.freedesktop.secrets` portal, and so does KeePassXC with Secret Service integration turned
on — one command reads all three.

```sh
# Debian/Ubuntu: apt install libsecret-tools    Fedora: dnf install libsecret
secret-tool store --label="Sirdar Zoho Desk token" service zoho-desk-token
```

`secret-tool store` reads the secret from the terminal. The attribute name must be `service`
and its value must match what the reference says after `keychain:` — that pair is the whole
lookup key.

On a headless box there is usually no Secret Service running, and `secret-tool` will say so
(`Cannot autolaunch D-Bus…`). Two ways out: unlock a keyring for the session with
`dbus-run-session` plus `gnome-keyring-daemon --unlock`, or skip the keyring and use a `file:`
or `cmd:` reference instead.

Where `secret-tool` is not installed but [`pass`](https://www.passwordstore.org/) is, Sirdar
uses `pass` instead, under a `sirdar/` folder so its entries stay apart from the rest of your
store:

```sh
pass insert sirdar/zoho-desk-token
```

Only the first line of the entry is the secret; anything below it is notes, the way `pass`
treats every entry. With both installed, `secret-tool` wins.

### Windows — the Credential Manager

Store a **generic** credential whose target name is the service:

```bat
cmdkey /generic:zoho-desk-token /user:sirdar /pass
```

Leave `/pass` bare and `cmdkey` prompts, keeping the secret out of the console history. The
`/user` value is not used for anything — the Credential Manager wants a user name on a generic
entry, and Sirdar reads only the password.

Reading it back is the awkward part, because `cmdkey` deliberately has no way to print a stored
password. The supported read path is the Win32 `CredRead` API, so Sirdar runs a short
PowerShell snippet that declares the P/Invoke with `Add-Type` and calls it:

```
powershell -NoProfile -NonInteractive -EncodedCommand <the snippet>
```

Nothing has to be installed for that — Windows PowerShell is enough — and the service name is
passed to the snippet in an environment variable rather than pasted into the script text, so a
service name with a quote in it cannot become PowerShell code. The snippet is passed
base64-encoded because the C# it carries is full of double quotes, which `-Command` would
mangle on the way through Go's argument quoting and PowerShell's own parser.

There is a second way to read the same store, which Sirdar does **not** use: the PowerShell
Gallery's [`CredentialManager`](https://www.powershellgallery.com/packages/CredentialManager)
module and its `Get-StoredCredential`. It is a fine tool, and if you have it installed you can
reach the same entry with a `cmd:` reference:

```yaml
token: cmd:powershell -NoProfile -Command "(Get-StoredCredential -Target zoho-desk-token).GetNetworkCredential().Password"
```

Sirdar's built-in path avoids it because it would put an install step from the Gallery between
a fresh Windows machine and a working `keychain:` reference.

## `file:` — a file holding the secret

```yaml
token: file:~/.config/sirdar/zoho-desk-token
```

A leading `~` expands to your home directory. The file holds the secret and nothing else; one
trailing newline is dropped, because every editor adds one, and anything beyond that is treated
as part of the secret.

**The file must not be readable by anyone but its owner.** On macOS, Linux and the BSDs, Sirdar
checks the permission bits and refuses the reference outright when any group or other bit is
set — it will not fall back to a warning:

```
apiToken file:/home/you/.sirdar-token: /home/you/.sirdar-token is readable or writable
beyond its owner (mode 0644); run: chmod 600 /home/you/.sirdar-token
```

So:

```sh
install -m 600 /dev/null ~/.config/sirdar/zoho-desk-token
# then paste the secret in, or:
printf '%s' "$SECRET" > ~/.config/sirdar/zoho-desk-token
```

Windows has no equivalent check: `os.Stat` there reports a mode synthesised from the read-only
attribute and the ACL that actually governs the file is invisible to it. Set the ACL yourself
(`icacls token /inheritance:r /grant:r "%USERNAME%":R`) or prefer `keychain:` on Windows.

## `cmd:` — a credential helper you already use

`cmd:COMMAND` runs `COMMAND` through `sh -c` (`cmd /C` on Windows) and takes its standard
output as the secret, trimmed. This is the scheme for everyone whose secrets already live in a
password manager or a vault:

```yaml
sources:
  helpdesk:
    adapter: zohodesk
    # 1Password CLI
    token: cmd:op read "op://Private/Zoho Desk/credential"

  tracker:
    adapter: jira
    # Bitwarden CLI — BW_SESSION must be in Sirdar's environment
    apiToken: cmd:bw get password jira-api-token

openai:
  # HashiCorp Vault
  apiKey: cmd:vault kv get -field=api_key secret/sirdar/openrouter
```

and, for a `pass` store organised however you like rather than under `sirdar/`:

```yaml
token: cmd:gopass show -o websites/zoho/desk-token
```

What to know about it:

- **10 seconds.** A helper that has not printed the secret by then is killed and the reference
  fails. That is enough for a Touch ID prompt or a YubiKey tap, and not enough for a hung
  network call to hold a run open.
- **The helper inherits Sirdar's environment**, which is how `op`, `bw` and `vault` find their
  session tokens (`OP_SERVICE_ACCOUNT_TOKEN`, `BW_SESSION`, `VAULT_TOKEN`). The agent session
  is a different matter: it gets a minimal environment and never sees any of this.
- **Standard output never leaves the process.** It is not logged, not echoed, and not put in an
  error message. Standard error *is* quoted in the error when the helper fails, because that is
  where `op` writes "not signed in" and the message is a diagnostic rather than the secret.
- **The command text itself appears in error messages**, since it is the reference that failed
  and an operator needs to know which one. Don't embed a secret in it — that would defeat the
  point of a reference.
- The shell is the platform's own, so a helper that needs a pipe (`vault … | jq -r .data.key`)
  works as written.

## `env:` — an environment variable

```yaml
token: env:ZOHO_DESK_TOKEN
```

Simplest, and the right answer in CI, where the secret arrives as a masked variable. On a
workstation it is the weakest of the four: the value sits in the environment of every process
you start from that shell.

Sirdar compensates where it can. Every variable named by an `env:` reference anywhere in
`sources.*` — `token`, `apiToken`, `pat`, `apiKey`, `oauthToken`, and all three parts of an
`auth:` grant — is stripped from the environment the agent process inherits, so a session that
can run shell commands cannot read the token back out of its own environment.

## When a reference fails

Every failure names the scheme, so it says which store to go and look in:

```
sources.helpdesk: token keychain:zoho-desk-token: credential not found: keychain:zoho-desk-token
  (no generic password for service zoho-desk-token in the login keychain)

sources.tracker: apiToken env:JIRA_TOKEN: credential not found: env:JIRA_TOKEN
  (environment variable JIRA_TOKEN is unset or empty)

openai.apiKey cmd:op read op://Private/OpenRouter/credential: exit status 1:
  [ERROR] 2026/09/11 12:00:00 account is not signed in
```

`sirdar doctor` resolves the credentials each built-in adapter names — and performs the Zoho
refresh grant — reporting a failure the same way, so a missing or revoked secret is caught
before a run spends an agent session on it. An `exec` adapter holds its own credentials and
answers for them itself.

`keychain:` on a platform with no store Sirdar can read (Plan 9, WASM) fails with
`keychain: refs are not supported on <goos>; use env:, file: or cmd:` rather than pretending
the entry is missing.
