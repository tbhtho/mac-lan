# Publishing signed updates

Publish from the existing [`tbhtho/mac-lan` repository](https://github.com/tbhtho/mac-lan).
Source, public release assets, and `updates/feed.json` share repository ID
1430459188. Do not create a separate downloads repository. The user's explicit
request for public source and automatic updates supersedes the earlier
manual-only decision.

## Local release workflow

Build Windows and both Mac layouts from clean, committed source using existing toolchains,
then run the publisher from the repository root with the authenticated publishing
account:

```powershell
./scripts/package-windows.ps1
./scripts/package-macos-app.ps1
./scripts/package-installers.ps1
./scripts/publish-release.ps1 -NotesFile <path-to-release-notes>
```

The publisher does not build binaries. It validates their metadata against the
exact clean HEAD, signs a local review feed, and generates
`artifacts/mac-lan-source-$version.zip` and `SHA256SUMS-$version.txt`. It creates
a draft release containing the Windows package, command-line bridge, canonical
app archive, app compatibility alias, source ZIP, and checksums. It verifies the
alias is byte-identical to the canonical app archive, signs the feed with the
actual release/asset IDs, then publishes the
release. Finally it commits only `updates/feed.json` with a plain natural subject
and pushes `main`. No paid CI or toolchain installation is part of this workflow.

Use `-PrivateKey <path>` to override `.release-private/updates.ed25519`.
Add `-ValidateOnly` for validation without GitHub mutations; it still generates
the local source ZIP, checksums, and review feed. Review those outputs before
publishing. A successful local validation is not a published release.

`package-installers.ps1` wraps those exact packages for first installs: a
per-user NSIS `mac-lan-setup.exe` (needs an existing NSIS 3 installation) and
`mac-lan-macos-arm64.dmg`, a read-only ISO 9660/Rock Ridge image with
`mac-lan.app` and an Applications link, written and read back by
`scripts/package-macos-dmg.py`. Both are release downloads only; the signed feed
keeps updating the installed EXE and app in place. Run the packaging scripts in
PowerShell 7 or Windows PowerShell 5.1. On a PC where an extensionless `python`
file shadows Python in PowerShell, alias `python` to `python.exe` first; the
installer script already calls `python.exe` directly.

Keep the source commit, package provenance, tag, and signed feed aligned. Publish
`mac-lan.exe`, `mac-lan-macos-arm64-commandline-preview.tar.gz`,
`mac-lan-macos-arm64.tar.gz`, and `MAC-DOW-macos-arm64-unsigned.tar.gz` before
the matching feed. Verify final asset names, byte counts, hashes, platform,
architecture, version, and commit against the actual uploaded bytes. After
publication, check the feed and all eight uploaded assets anonymously. Source changes alone
are not an application release; immutable release tags and assets must not be
reused or replaced.

The feed URL is
`https://raw.githubusercontent.com/tbhtho/mac-lan/main/updates/feed.json`.
Applications use it without GitHub sign-in. Existing alpha.13 and older users
must install alpha.14 on both computers once because their old feed was
withdrawn. Keep old private bootstrap releases private: their embedded diagnostic
configuration is unsuitable for public assets.

## Key custody

The applications trust an Ed25519 public key in their build configuration. The
private key stays on the publishing computer outside version control. Generate
it once using the native Go target (clear a `GOOS=darwin` cross-build setting
first):

```powershell
go run -C host ./cmd/macdow-release keygen --key ../.release-private/updates.ed25519
```

This prints only the base64 public key. Add that public key to the application's
`updatePublicKey` build setting before building releases. The key file contains
64 raw private-key bytes. Existing files are never overwritten by `keygen`.
Back up the private key securely; losing it prevents signing updates for existing
installations. Never commit, upload, print, or distribute that file. On Windows,
keep its directory restricted to the publishing account through Windows file
permissions; Unix key files are created with mode `0600`.

## Prepare or inspect a feed locally

The lower-level preparation command does not publish. Supply actual release and
asset IDs when preparing the final feed; the following IDs and commit are only
placeholders:

```powershell
./scripts/prepare-update-feed.ps1 `
  -WindowsArtifact artifacts/mac-lan.exe `
  -MacArtifact artifacts/update-public/mac-lan-macos-arm64-commandline-preview.tar.gz `
  -MacAppArtifact artifacts/update-public/MAC-DOW-macos-arm64-unsigned.tar.gz `
  -ReleaseTag v0.1.0-alpha.15 `
  -Commit 0123456789abcdef0123456789abcdef01234567 `
  -ReleaseId 100 -WindowsAssetId 101 -MacAssetId 102 -MacAppAssetId 103 `
  -PreviousFeed artifacts/previous-feed.json
```

Omit `-PreviousFeed` for the first feed. A previous feed's signature and identity
are verified before its history is preserved. Review-only IDs must be replaced
with final IDs before publication.

Preparation hashes the three feed artifacts and writes UTF-8 `payload.json` and signed
`feed.json` under `artifacts/update-feed`. The signature covers the exact payload
bytes, including whitespace. The manifest binds product, repository ID, version,
source commit, asset names, byte counts, and SHA-256 hashes. Download locations
are fixed in the applications, not accepted from the feed.

Alpha.14's feed validator recognizes only the existing Windows, command-line
preview, and `MAC-DOW-macos-arm64-unsigned.tar.gz` app names. Keep the canonical
`mac-lan-macos-arm64.tar.gz` name out of the shared feed, even though it is the
primary manual download. Alpha.15's app selects the known app alias; alpha.14's
host selects the command-line bridge, which installs and opens a separate app
on its next normal launch. Do not create a separate feed or signing key for
this transition.

The Mac gate requires `BUILD-PROVENANCE.json` with `publicUpdate:true` and scans
archive contents for concrete embedded Discord webhook credentials. Build the
public app and bridge using `./scripts/package-macos-app.ps1`. The underlying
`package-macos-preview.ps1` still supports standalone bridge packaging; public
packaging is the default, and `-PublicUpdate` remains an optional compatibility flag. A
private bootstrap requires explicit `-PrivateBootstrap` and is written only
to `artifacts/private-bootstrap`. Private
bootstrap packages are refused, even when their filename matches a public asset.
The public binary reuses the Mac's existing local diagnostic configuration;
Windows diagnostics remain local-only.

The feed expires after 30 days. Publish a freshly signed feed before expiry,
even when keeping the same binary release. Clients reject expired or modified
feeds. Preserve earlier signed release entries when preparing a new release.
To refresh expiry without a new release, update only the top-level `publishedAt`
and `expiresAt` in the previous decoded payload and sign those exact bytes with
the local tool; the release entries remain unchanged.

For manual review or signing of an exact payload:

```powershell
go run -C host ./cmd/macdow-release sign --key ../.release-private/updates.ed25519 --payload ../artifacts/update-feed/payload.json --out ../artifacts/update-feed/feed.json
go run -C host ./cmd/macdow-release verify --feed ../artifacts/update-feed/feed.json
```

`sign` refuses a private key that differs from the public key configured in the
application. `verify` uses that configured public key. Feed signatures establish
publisher and artifact integrity; they do not provide Windows code signing,
Apple Developer ID signing, notarization, native Mac execution, or measured LAN
performance.
