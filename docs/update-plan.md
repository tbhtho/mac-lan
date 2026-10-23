# MAC-DOW updates milestone

Baseline f14af5137eae989fa89a6dbefffcf9b2a249ab52 fast-forwarded cleanly. Only MAC-DOW, no AVE changes.

Shared Go updater for Windows helper and Mac host. Existing gh credentials only; never acquire/embed/persist tokens. Fixed github.com, repository ID 1430459188, tbhtho/MAC-DOW. Strict semantic version, stable/preview, exact platform/architecture, exact asset name/size/SHA-256 and immutable release identity. Reject kits, drafts, downgrades, duplicates and missing digests.

Windows startup/periodic checks (opt-out), Updates dialog; foreground Mac startup check and --updates console. Check, Stage Update, explicit confirmed Apply/Relaunch and rollback. No login, browser-based token gathering or external-link button pretending to auto-update.

Authenticated gh downloads to owned staging; safe ZIP/tar extraction (traversal/links/duplicates/bombs bounded). Revalidate metadata/hash/commit at apply. Automatic replacement requires cryptographically signed GitHub release attestation via gh release verify-asset. Existing mutable/unattested releases stop at staged/manual fallback. This provenance is distinct from OS signing/notarization. No new signing keys or security-setting changes.

Recognized portable install only, no .git/root/arbitrary-folder replacement. Outside-install helper waits for parent exit, stages sibling, journals, swaps old to backup, restores old on failure. External settings preserved, old folder retained for manual recovery. Explicit rollback re-downloads the older signed release and verifies it independently; journal hashes and backup bytes are never executable trust anchors. No privileged installer or automatic capture-host restart.

Use synthetic metadata/archives/temp installations and injected verifier in tests, never product CLI trust bypass or replacement of actual user app. Publish tested scoped new prerelease without altering alpha.2 assets. Native Mac execution/build validation stays explicitly separate.

Verification prerequisite: existing GitHub CLI >=2.93.0. Older versions may check/download but never run signature verification (GHSA-8xvp-7hj6-mcj9). No global tool upgrade/auth change performed.
