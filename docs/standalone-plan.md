# Standalone apps and in-app updates

Goal: LAN screen mirroring with a double-click Mac host, a single portable Windows EXE, and updates downloaded inside both apps.

User delegated technical delivery choices after encountering the shipped manual --listen requirement. Keep repository/downloads private for now; do not publish, change authentication, create signing keys, commit or disturb existing setup WIP. Continue in this checkout. Existing signature verification remains mandatory for executable replacement.

Design: Windows bundles .NET/native support, viewer resources and the Go discovery/updater helper into MAC-DOW.exe. Private application caches hold extracted runtime resources; no adjacent dependency folder is required. An embedded package identity (product/version/platform/commit) identifies the EXE without executing it. Windows updates replace only that file, never its containing Downloads/Desktop directory. Existing legacy folder installs remain recognized separately.

Mac: MAC-DOW Host.app contains the AppKit launcher, Go host and Swift capture helper. Existing automatic private-interface selection removes manual --listen from normal launch. Native build produces the bundle and preserves native self-tests. Finder sees one application. App launches idle, permissions remain user-controlled, Bonjour advertises only after Start sharing, and approved streams remain view-only until armed.

Updates: check at startup and every six hours while open; download available verified releases automatically when enabled. Show Restart to update; never replace/restart during an active session without that user action. External settings survive, previous executable/bundle retained, failures restore the previous installation. Reopen idle/view-only. Both platforms expose manual Check and automatic-check preference. Private GitHub access and signed release availability are real deployment prerequisites, not simulated by tests. No public downloads chosen on the user's behalf.

Implementation (inline):
- [x] Regression tests for raw Windows EXE package identity, file-only replacement, restored backups and .app payload/launch layout. Observed red then green.
- [x] Embed Windows resources/helper, publish exactly one EXE, add non-UI package diagnostics and fresh-directory smoke verification.
- [x] Implement Mac .app assembly and update dispatch/UI source, preserving automatic LAN setup and explicit consent. Native compile is still pending.
- [x] Connect automatic download + Restart actions on Windows and Mac; keep update failures separate from LAN sharing. Real apply remains gated by release provenance.
- [x] Update documentation; full headless suite and Windows package smoke pass; Darwin Go cross-compile passes.
- [x] Fresh-context final code review: no critical/important findings.
- [ ] Native Swift/.app compile, Mac permissions and end-to-end mirroring acceptance. No Mac available to this chat; billing allowance is inaccessible.

Release ruling: user explicitly requested pushing a GitHub release to test themselves. Commit/push scoped source and publish a Windows EXE plus clearly marked cross-compiled command-line Mac fallback (unchanged previously native-built capture helper), source, checksums. Do not label the unavailable .app as delivered, do not run a potentially paid workflow, and do not weaken update verification.

Review focus: Downloads/Desktop siblings must survive Windows update; linked install paths refused; immutable staged release/hash reverified; Mac worker must outlive app exit and reopen app idle; no helper/UI permission identity claim until native validation.

References: .NET single-file deployment https://learn.microsoft.com/en-us/dotnet/core/deploying/single-file/overview ; Apple bundle structure https://developer.apple.com/documentation/bundleresources/placing-content-in-a-bundle
