# Standalone delivery verification

2026-10-23, Windows development environment. No UI, real capture or injected input was started.

- Regression tests first failed for unsupported raw EXE staging and missing .app layout. They now cover EXE apply/rollback, preservation of neighboring files and old EXE bytes, malformed identity/wrong version/commit rejection, app bundle metadata layout, idle Finder relaunch, and refusal to replace a legacy install folder with an EXE. That refusal also failed before its guard was implemented.
- Full headless suite passed after initial implementation: 32 core checks, Node viewer/decoder race tests, Go host/discovery/updater tests, go vet, Windows Release build with zero warnings/errors. Final release rebuild/check is recorded in the release notes.
- Single-file publishing verified exactly one MAC-DOW.exe. Initial failures identified single-file linker lockfile requirements and loose PDB/XML outputs; build flags and lockfile corrected.
- EXE copied alone into a fresh directory with an unrelated keep.txt. Package diagnostics found the embedded viewer/helper and reported UI/hooks/capture false; keep.txt unchanged.
- Darwin arm64 Go host cross-compiles. This is not native runtime acceptance. AppKit launcher and .app packaging remain uncompiled here; native capture helper from alpha.2 is historical evidence only.
- Current GitHub CLI 2.88.0 is below the release-signature verification floor. No vulnerable signature-verification command was run. Current published releases have no signed provenance, so automatic apply remains blocked. No credentials, repository visibility, signing keys or security settings changed.
- GitHub billing API still returns 404/missing user scope. No auth expansion or potentially paid native workflow run was attempted. Mac .app cannot be distributed as built/verified from this environment.

Decisions: keep private distribution rather than making app downloads public; retain signature gating rather than silently executing unattested downloads; preserve existing WIP and old release assets. The release request subsequently authorizes a scoped source commit/push/prerelease. Native and end-to-end acceptance remain outstanding.
