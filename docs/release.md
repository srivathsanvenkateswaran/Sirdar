# Cutting a release

Releases are built by [goreleaser](https://goreleaser.com) from a tag, driven by
`.github/workflows/release.yml`. Nothing publishes itself: every release opens as a
draft, and a human reads it and clicks Publish.

## Cutting one

1. Make sure `main` is what you want to ship, and decide the version (semver,
   e.g. `1.2.3`; a pre-1.0 project can also tag `0.x.y`).
2. From `main`, tag and push:

   ```
   git tag v1.2.3
   git push origin v1.2.3
   ```

3. Watch the `release` workflow run on the tag. It has two jobs:
   - `goreleaser` builds the `sirdar` CLI for darwin/linux/windows on
     amd64/arm64, packages deb/rpm for Linux, writes checksums and a
     changelog grouped into Features/Fixes (commits starting `feat:`/`fix:`;
     `docs:` commits are dropped from the notes entirely), and opens the
     GitHub release as a **draft**. If `HOMEBREW_TAP_GITHUB_TOKEN` is set and
     this isn't a prerelease, it also pushes the Homebrew formula to the tap
     repo (see below) — draft releases are skipped for that push, so nothing
     reaches the tap until you publish.
   - `desktop` builds the Wails desktop app on macOS (Apple silicon and
     Intel, on separate runners), Windows, and Linux, zips each build's
     output, and uploads the zips onto the same draft release. The names are
     `sirdar-desktop_<tag>_<os>_<arch>.zip` with `<os>` one of `darwin`,
     `windows`, `linux` and `<arch>` GitHub's `runner.arch` lower-cased, so
     `arm64` or `x64` (not Go's `amd64`): `darwin_arm64`, `darwin_x64`,
     `windows_x64`, `linux_x64`. The landing page's download buttons resolve
     exactly those four names, so a rename here is a change to
     `site/download.js` too.
4. Once both jobs finish, open the draft release on GitHub. Check the
   changelog, that six CLI archives plus checksums plus the deb/rpm packages
   plus four desktop zips are attached, then click **Publish release**.
5. Publishing is what makes the tag's artifacts and the Homebrew formula (if
   the tap step ran) publicly visible. Nothing before this step is
   downloadable by anyone but you.

Only tag from `main`, and only tag commits you'd be comfortable shipping —
a draft is a safety net for review, not for fixing a bad build after the
fact; a bad tag has to be deleted and re-pushed.

## The Homebrew tap

`.goreleaser.yaml`'s `brews:` pipe pushes a formula to
`srivathsanvenkateswaran/homebrew-sirdar`, a separate repo Homebrew's `brew tap`
convention expects. **That repo does not exist yet.** Before the first release
that should reach Homebrew:

1. Create an empty public GitHub repo named `homebrew-sirdar` under the
   `srivathsanvenkateswaran` account (no need to pre-populate anything —
   goreleaser creates and commits `Formula/sirdar.rb` itself).
2. Create a token that can push to that repo — a classic PAT with `repo`
   scope, or a fine-grained token scoped just to `homebrew-sirdar` with
   contents read/write — and add it as the `HOMEBREW_TAP_GITHUB_TOKEN`
   secret on the `Sirdar` repo (Settings → Secrets and variables → Actions).
   The default `GITHUB_TOKEN` can't be used here: it can't write to a
   different repository.
3. Until both of those exist, the `brews` pipe has nowhere to push to for a
   real (published) release. For a draft it's moot either way:
   `skip_upload: auto` skips the push for drafts and prereleases regardless.

Once the tap exists and has a formula, installing is:

```
brew tap srivathsanvenkateswaran/sirdar
brew install sirdar
```

(Homebrew maps the tap name `sirdar` to the repo `homebrew-sirdar`
automatically.)

## Installing a release

- **Homebrew** (once the tap exists and a release has been published):
  `brew tap srivathsanvenkateswaran/sirdar && brew install sirdar`
- **`go install`** (always available, any released or unreleased commit):
  `go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest`
- **Direct download**: grab the archive for your OS/arch from a release's
  Assets (`sirdar_<version>_<os>_<arch>.tar.gz`, or `.zip` on Windows),
  extract it, and put `sirdar` on your `PATH`. `checksums.txt` in the same
  release verifies the download.
- **deb/rpm** (Linux): the release's Assets also carry `.deb` and `.rpm`
  packages built by `nfpm`, install directly with `dpkg -i` / `rpm -i` (or
  your distro's front end).
- **Desktop app**: download the zip for your OS from the release's Assets
  (`sirdar-desktop_<tag>_<os>_<arch>.zip`; on a Mac, `darwin_arm64` for Apple
  silicon and `darwin_x64` for Intel), unzip it, and run the app inside. The
  landing page picks the right one from the visitor's browser.
  The desktop builds are **unsigned** — no Apple notarization, no Windows
  code-signing certificate. On macOS, Gatekeeper will refuse to open it with
  a plain double-click; either right-click the app and choose **Open** (and
  confirm once in the dialog that appears), or clear the quarantine flag
  from a terminal:

  ```
  xattr -d com.apple.quarantine /path/to/Sirdar.app
  ```

  Windows SmartScreen may show a similar "unknown publisher" warning; choose
  **More info → Run anyway**. The Windows section below has the detail.

## Windows

The CLI is an ordinary static binary: unzip `sirdar_<version>_windows_amd64.zip`
(or `_arm64`), put `sirdar.exe` somewhere on your `PATH`, and it runs. Nothing
else is needed.

The desktop app has one requirement and one warning.

**The WebView2 runtime.** The app is a native window around Microsoft's
WebView2, the same engine Edge uses, and Wails does not bundle it. Windows 11
and every current Windows 10 ship it preinstalled, so on a machine that takes
Windows Update this is already satisfied. On one that does not — a fresh LTSC
or Server image, most often — the app opens a window and then fails to draw
anything. Install the **Evergreen Bootstrapper** from
<https://developer.microsoft.com/microsoft-edge/webview2/> and start it again.

**SmartScreen.** The build is unsigned: there is no Windows code-signing
certificate on this project, so `Sirdar.exe` has no publisher and
Microsoft Defender SmartScreen shows "Windows protected your PC" the first
time it runs. Choose **More info → Run anyway**. A download from a browser
also carries the Mark of the Web; if Windows refuses to run it outright,
right-click the `.exe` → **Properties** → tick **Unblock** → **OK**. Both
prompts go away once someone buys a certificate and the release workflow
signs with it; until then this is what an unsigned binary looks like and
telling people to expect it is better than having them wonder.

**Where it keeps things.** The workspace registry and the golden set live
under `%USERPROFILE%\.sirdar`, the same relative place they take on the
other platforms. `keychain:` credential references read the **Windows
Credential Manager** through a PowerShell `CredRead` call — store one with
`cmdkey /generic:<name> /user:<anything> /pass`, or with the Credential
Manager control panel — and `env:`, `file:` and `cmd:` references work as
they do everywhere. `docs/credentials.md` has the detail.

**Building it.** `wails build -platform windows/amd64` cross-compiles from
macOS or Linux — Wails v2 needs no cgo for the Windows target — so
`make desktop-windows` produces a real `Sirdar.exe` on a machine that has no
Windows at all, and `make dist-desktop` zips it alongside the host build.
The `windows` job in `.github/workflows/ci.yml` builds it natively on
`windows-latest` and uploads the `.exe` as an artifact on every push, which
is the copy to grab when you want to try a branch without cutting a tag.

## Local dry runs

- `make version` — prints the version a local build would stamp in
  (`git describe --tags --always`), without needing a tag on `HEAD`.
- `make release-snapshot` — runs `goreleaser build --snapshot --clean`,
  producing all six CLI binaries into `dist/` without touching GitHub or the
  tap. Delete `dist/` when done looking.
- `make dist-desktop` — builds and zips the desktop app for whichever OS/arch
  you're running the command on, plus the cross-compiled `windows_amd64` zip,
  both under the names the workflow's desktop job uploads. Linux is the one
  platform this cannot stand in for; that's what the workflow's matrix is for.
- `make desktop-windows` — just the Windows cross-build, straight into
  `desktop/build/bin/Sirdar.exe`.
- `goreleaser check` validates `.goreleaser.yaml` without building anything.

## What's in `CHANGELOG.md`

`CHANGELOG.md` is hand-maintained under an `Unreleased` heading between
releases; the goreleaser changelog attached to each GitHub release is
generated separately, straight from conventional-commit-prefixed git log
messages, and is not meant to replace it.
