# MAC-DOW implementation plan

Spec: design.md. Execute inline in the dedicated new workspace; no other repositories or global auth/configuration changes.

- [x] Safety core: tests first for focus/connection/control gating, release on every transition, physical key mapping, aspect-fit coordinates, monitor fallback/DPI and topology changes.
- [x] WPF shell and pinned TLS client: tab strip, explicit pairing, bounded frames, no reconnect auto-control, separate App Priority window and input hooks only on explicit arm. Compile without opening UI.
- [x] Mac host source (native compilation blocked): tests first for private addressing, certificate/auth, consent, single session, frame bounds and watchdog semantics; Go WSS transport plus Swift capture/input helper with no prompting. Build/test host headlessly.
- [ ] Packaging and documentation: unsigned portable Windows archive, Apple Silicon build script/manual CI, source and third-party notices, exact verification and manual hardware checklist.
- [ ] Review, private publication, prerelease: inspect staged files for secrets/generated clutter; verify authenticated identity and private visibility, non-force push, publish verified artifacts. Do not invoke billable CI when free allowance cannot be confirmed.

Review focus: App Priority never exposes desktop on close; input lease is revoked before focus/tab transitions; slow links cannot queue unbounded frames; DPI handles negative-origin monitors; uncertain connection failure cannot leave hooks installed.
