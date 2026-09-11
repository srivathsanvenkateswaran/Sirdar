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
   - `desktop` builds the Wails desktop app on macOS, Windows, and Linux,
     zips each platform's output, and uploads the zips onto the same draft
     release.
4. Once both jobs finish, open the draft release on GitHub. Check the
   changelog, that six CLI archives plus checksums plus the deb/rpm packages
   plus three desktop zips are attached, then click **Publish release**.
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
  (`sirdar-desktop_<tag>_<os>_<arch>.zip`), unzip it, and run the app inside.
  The desktop builds are **unsigned** — no Apple notarization, no Windows
  code-signing certificate. On macOS, Gatekeeper will refuse to open it with
  a plain double-click; either right-click the app and choose **Open** (and
  confirm once in the dialog that appears), or clear the quarantine flag
  from a terminal:

  ```
  xattr -d com.apple.quarantine /path/to/Sirdar.app
  ```

  Windows SmartScreen may show a similar "unknown publisher" warning; choose
  **More info → Run anyway**.

## Local dry runs

- `make version` — prints the version a local build would stamp in
  (`git describe --tags --always`), without needing a tag on `HEAD`.
- `make release-snapshot` — runs `goreleaser build --snapshot --clean`,
  producing all six CLI binaries into `dist/` without touching GitHub or the
  tap. Delete `dist/` when done looking.
- `make dist-desktop` — builds and zips the desktop app for whichever OS/arch
  you're running the command on (not all three platforms; that's what the
  workflow's matrix is for).
- `goreleaser check` validates `.goreleaser.yaml` without building anything.

## What's in `CHANGELOG.md`

`CHANGELOG.md` is hand-maintained under an `Unreleased` heading between
releases; the goreleaser changelog attached to each GitHub release is
generated separately, straight from conventional-commit-prefixed git log
messages, and is not meant to replace it.
