# MAC-DOW updates

**Current distribution:** the user chose manual reinstalls from
[`tbhtho/MAC-DOW` releases](https://github.com/tbhtho/MAC-DOW/releases).
The separate public repository and update feed are withdrawn. The design below
is retained as implementation history, not an active publishing plan.

The Windows single EXE and Apple Silicon command-line preview use the shared Go
updater. Starting with alpha.12, production updates come from the public
`tbhtho/MAC-DOW-Downloads` binary repository. Source remains in `tbhtho/MAC-DOW`,
repository ID 1430459188. End users need neither GitHub CLI nor GitHub sign-in.

The applications check at startup and every six hours, download newer releases
in the selected channel, and offer **Restart to update**. Windows checks can be
disabled in Updates. The Mac host offers restart only while no connection is
active; choosing Later keeps sharing available. Restart requires fresh pairing,
viewing consent and click activation before remote control resumes.

An Ed25519 public key is compiled into both apps. The publisher keeps its private
key outside version control. Clients verify the exact signed feed bytes before
parsing, expiry, product/source identity, channel/version, release and asset IDs,
commit, exact platform filename, byte count and SHA-256. Feed/download origins
and bounded HTTPS redirects are fixed. Apply fetches and authenticates current
metadata again and rehashes downloaded bytes. Expired, modified, revoked or
downgraded metadata cannot authorize replacement. This replaces the earlier
GitHub CLI attestation design; it is separate from OS signing and notarization.

Updates replace recognized portable installations only. Mac previews require a
dedicated extracted folder, valid arm64 executable and matching package marker.
Broad user/system folders, links and unrelated files block replacement. Windows
replaces only its identified standalone EXE and preserves neighboring files.
The verified detached worker waits for the parent to exit, retains the previous
installation as backup, journals its work and restores it on a failed swap.
External settings stay in place. Rollback independently verifies an older release
still present in the current signed feed.

The existing user's final private alpha.12 bootstrap preserves the Mac diagnostic
destination in private per-user configuration. Public update packages contain no
embedded destination and reuse that configuration. Every apply entry point
persists the bootstrap configuration before replacement. Windows sends no webhook
diagnostics. The publishing tool refuses private Mac archives and archives
containing webhook credentials.

Build and publish both assets before publishing the matching signed feed. Never
reuse release tags or replace published assets. See
[publishing instructions](update-feed-publishing.md) for signing, feed renewal,
and key custody. Updating source alone does not distribute an app update.

Local validation uses isolated installations and synthetic signatures; a real
public-feed canary supplements these checks at publication. Native Mac
apply/relaunch remains a separate validation step. Windows tests cannot establish
Darwin process and filesystem behavior. The Mac menu-bar `.app` is still
source-only, so this updater supports the actual shipped command-line preview.
No automatic paid CI is required.
