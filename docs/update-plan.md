# mac-lan updates

The public source, release assets, and signed update feed all live in the existing
[`tbhtho/mac-lan` repository](https://github.com/tbhtho/mac-lan), repository ID
1430459188. This is the renamed original repository, with its source history
preserved. The separate downloads repository is not used.

Install alpha.14 or later on both computers once from the public releases.
Earlier builds used a withdrawn feed and cannot discover this distribution.
From alpha.14, the Windows single EXE and Apple Silicon command-line preview use
the shared Go updater without GitHub CLI or GitHub sign-in. Alpha.15 adds the
menu-bar app and a compatibility bridge from the command-line installation.

The applications check at startup and every six hours, download newer releases
in the selected channel, and offer **Restart to update**. Windows checks can be
disabled in Updates. Background downloads do not interrupt an active connection.
The menu-bar app shows a ready update in its menu and installs only after an
explicit restart choice, gracefully stopping sharing first. The older command-line
host offers restart only while idle. Restart requires fresh pairing, viewing consent and click activation
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

The primary public downloads are `mac-lan.exe` for Windows x64 and
`mac-lan-macos-arm64.tar.gz` for Apple Silicon macOS 14+. The latter contains
`mac-lan.app`, whose cross-compiled Go launcher builds its verified native
AppKit menu using existing Apple tools at first launch. The capture helper
builds at first video setup. No Terminal window is needed. macOS can require
deliberate Screen Recording and Accessibility grants for the app instead of the
earlier Terminal identity. Legacy internal identifiers and settings/cache paths
remain unchanged.

The release also contains `mac-lan-macos-arm64-commandline-preview.tar.gz` and
the byte-identical app alias `MAC-DOW-macos-arm64-unsigned.tar.gz`. Alpha.14
strictly validates all feed asset names, so the shared feed advertises Windows,
the command-line bridge, and the known app alias. The canonical app archive is
available for manual download and must not appear as an unknown fourth feed
asset. The new app chooses the app asset; an alpha.14 command-line host chooses
the bridge. On the updated host's next normal launch, it installs and opens a
separate `~/Applications/mac-lan.app` while preserving the old preview folder.
It requires a matching valid preview and preserves unrelated existing apps.

Updates replace recognized installations only. Mac previews require a
dedicated extracted folder, valid arm64 executable and matching package marker.
Broad user/system folders, links and unrelated files block replacement. Windows
replaces only its identified standalone EXE and preserves neighboring files.
App updates validate the marked app bundle, exact entry point/helper layout,
source commit and arm64 binaries before replacing that app. The updater uses
the actual app path and relaunches through LaunchServices.
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

Build and publish all binary assets before publishing the matching signed feed. Never
reuse release tags or replace published assets. Use the local
`./scripts/publish-release.ps1` workflow described in the
[publishing instructions](update-feed-publishing.md). Updating source alone does
not distribute an app update. No paid CI is required.

Local validation uses isolated installations and synthetic signatures; anonymous
checks of the published feed and artifact bytes supplement those tests. Native
Mac apply/relaunch remains a separate validation step. Windows tests cannot
establish Darwin process and filesystem behavior, first-launch AppKit compilation,
menu visibility, permission attribution or native capture/input. App and bridge
archive checks do not establish those runtime behaviors.

The earlier manual-only decision and separate-downloads-repository design are
historical. The user's explicit public-repository and automatic-update request
supersedes that distribution policy without changing the project's headless
validation or OS-permission boundaries.
