# Publishing signed updates

**Inactive:** the user chose manual reinstalls from
[`tbhtho/MAC-DOW` releases](https://github.com/tbhtho/MAC-DOW/releases).
The separate public repository and update feed are withdrawn. Keep these tooling
notes for reference; do not publish or recreate that distribution without a new
explicit user request.

The applications trust an Ed25519 public key in their build configuration. The
private signing key stays on the publishing computer. The local publisher does
not upload artifacts, modify a GitHub repository, or sign in to GitHub.

Generate the publishing key once from the repository root, using the native Go
target (clear any `GOOS=darwin` cross-build setting first):

```powershell
go run -C host ./cmd/macdow-release keygen --key ../.release-private/updates.ed25519
```

This prints only the base64 public key. Add that public key to the application's
`updatePublicKey` build setting before building releases. The key file contains
64 raw private-key bytes. Existing files are never overwritten by `keygen`.
Back up the private key securely; losing it prevents signing updates for existing
installations. Do not commit, upload, print, or distribute that file. On Windows,
keep its directory restricted to the publishing account through Windows file
permissions; Unix key files are created with mode `0600`.

Prepare both public binaries and identify their source commit, release tag, and
release/asset IDs. A Mac package built with an embedded private diagnostics
webhook is not suitable for public downloads. Use the distinct public package.

```powershell
./scripts/prepare-update-feed.ps1 `
  -WindowsArtifact artifacts/MAC-DOW.exe `
  -MacArtifact artifacts/update-public/MAC-DOW-macos-arm64-commandline-preview.tar.gz `
  -ReleaseTag v0.1.0-alpha.12 `
  -Commit 0123456789abcdef0123456789abcdef01234567 `
  -ReleaseId 100 -WindowsAssetId 101 -MacAssetId 102 `
  -PreviousFeed artifacts/previous-feed.json
```

Omit `-PreviousFeed` for the first feed. The publisher verifies a previous feed's
signature and identity before preserving its history. Supply actual public
release/asset IDs when publishing. Positive review-only IDs are acceptable when
preparing a local draft; rebuild the signed feed with final IDs before uploading.
Published release tags must be unique and assets immutable.

The script hashes the artifact bytes and writes UTF-8 `payload.json` and signed
`feed.json` under `artifacts/update-feed`. The signature covers the exact payload
bytes, including whitespace. The manifest binds product, private source repository
ID, version, source commit, asset names, byte counts, and SHA-256 hashes. Download
locations are fixed in the applications, not accepted from the feed.
The Mac manifest gate requires `BUILD-PROVENANCE.json` with `publicUpdate:true`
and scans archive contents for concrete embedded Discord webhook credentials.
Build this archive using `./scripts/package-macos-preview.ps1 -PublicUpdate`.
Private bootstrap packages are refused, even when they have the same filename.

The feed expires after 30 days. Publish a freshly signed feed before expiry, even
when keeping the same binary release. Clients reject expired or modified feeds.
Preparing a new release preserves previous signed release entries. To refresh
expiry without a new release, update only the top-level `publishedAt` and
`expiresAt` in the previous decoded payload and sign those exact bytes with the
local tool; the release entries remain unchanged.

For manual review or signing of an exact payload:

```powershell
go run -C host ./cmd/macdow-release sign --key ../.release-private/updates.ed25519 --payload ../artifacts/update-feed/payload.json --out ../artifacts/update-feed/feed.json
go run -C host ./cmd/macdow-release verify --feed ../artifacts/update-feed/feed.json
```

`sign` refuses a private key that differs from the public key configured in the
application. `verify` uses that same configured public key. Publishing the public
binary repository and its feed requires the user's authorization; these commands
only prepare local files.
