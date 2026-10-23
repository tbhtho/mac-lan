# mac-lan updates

The public source, release assets, and signed update feed all live in the existing
[`tbhtho/mac-lan` repository](https://github.com/tbhtho/mac-lan), repository ID
1430459188. This is the renamed original repository, with its source history
preserved. The separate downloads repository is not used.

Install alpha.14 or later on both computers once from the public releases.
Earlier builds used a withdrawn feed and cannot discover this distribution.
From alpha.14, the Windows single EXE and Apple Silicon command-line preview use
the shared Go updater without GitHub CLI or GitHub sign-in.

The applications check at startup and every six hours, download newer releases
in the selected channel, and offer **Restart to update**. Windows checks can be
disabled in Updates. An active connection is not interrupted. The Mac host offers
restart only while no connection is active; choosing Later keeps sharing
available. Restart requires fresh pairing, viewing consent and click activation
before remote control resumes.

An Ed25519 public key is compiled into both apps. The private signing key stays
outside version control on the publishing computer. Clients obtain the feed from
`https://raw.githubusercontent.com/tbhtho/mac-lan/main/updates/feed.json` and
verify its exact signed bytes before parsing, expiry, product/source identity,
channel/version, release and asset IDs, commit, exact platform filename, byte
count and SHA-256. Feed/download origins and bounded HTTPS redirects are fixed.
Apply fetches and authenticates current metadata again and rehashes downloaded
bytes. Expired, modified, revoked or downgraded metadata cannot authorize
replacement. This is separate from OS signing and notarization.

Public asset names are `mac-lan.exe` for Windows x64 and
`mac-lan-macos-arm64-commandline-preview.tar.gz` for Apple Silicon macOS 14+.
The Mac archive contains the existing `MacDowHost` executable. Legacy internal
identifiers, settings/cache paths, and permission identities are retained so a
branding change does not discard configuration or disrupt permissions.

Updates replace recognized portable installations only. Mac previews require a
dedicated extracted folder, valid arm64 executable and matching package marker.
Broad user/system folders, links and unrelated files block replacement. Windows
replaces only its identified standalone EXE and preserves neighboring files.
The verified detached worker waits for the parent to exit, retains the previous
installation as backup, journals its work and restores it on a failed swap.
External settings stay in place. Rollback independently verifies an older release
still present in the current signed feed.

The existing user's private Mac bootstrap preserved its diagnostic destination
in private per-user configuration. Public packages contain no embedded
destination and reuse that configuration. Every apply entry point persists the
bootstrap configuration before replacement. Windows sends no webhook
diagnostics. The publishing tool refuses private Mac archives and archives
containing webhook credentials. Old private bootstrap releases must stay private;
they are not the public upgrade path.

Build and publish both assets before publishing the matching signed feed. Never
reuse release tags or replace published assets. Use the local
`./scripts/publish-release.ps1` workflow described in the
[publishing instructions](update-feed-publishing.md). Updating source alone does
not distribute an app update. No paid CI is required.

Local validation uses isolated installations and synthetic signatures; anonymous
checks of the published feed and artifact bytes supplement those tests. Native
Mac apply/relaunch remains a separate validation step. Windows tests cannot
establish Darwin process and filesystem behavior. The Mac menu-bar `.app` is
still source-only, so this updater supports the shipped command-line preview.

The earlier manual-only decision and separate-downloads-repository design are
historical. The user's explicit public-repository and automatic-update request
supersedes that distribution policy without changing the project's headless
validation or OS-permission boundaries.
