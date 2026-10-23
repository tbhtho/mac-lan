# Guided LAN setup Implementation Plan
> For agentic workers: execute inline with test-driven-development and final independent review.
Goal: remove Terminal/IP/token work from normal setup.
Architecture: native AppKit launcher controls existing Go TLS server by inherited JSON pipes; Bonjour discovery only locates endpoints, explicit physical-user approval authenticates them.
Tech stack: Swift AppKit/Foundation NetService, Go TLS/Gorilla WebSocket/libp2p zeroconf, .NET WPF.
Spec: docs/setup-design.md
Constraints: Mac offline/unapproved; 17GiB free, no duplicate release bundles; no actual UI/capture/hooks; no AVE/FluxFrame changes; no PRs or credential/firewall changes.

Review focus: multi-NIC/VPN/no-interface; forged/stale advertisements; stale/timeout consent and pipe EOF; stop while input held; Mac bundle identity/permissions.
1. Host setup + consent protocol
- [ ] Add failing injected-NIC selection and pipe-consent tests; run red.
- [ ] Implement host --interfaces, --gui --listen auto, selected private bind, bounded structured consent and graceful stop.
- [ ] Add synthetic TLS guided-pair approval/denial/rate/malformed/stop tests; run green.
2. Discovery + Windows guided connect
- [ ] Add failing record validation/dedupe/identity-code tests.
- [ ] Implement bounded DNS-SD browser CLI and WPF Find/select/compare/request-number flow.
- [ ] Full headless suite, no real multicast discovery during tests.
3. Native host app + packaging
- [ ] Implement AppKit permission/status/start/stop/consent UI and NetService publication, idle by default.
- [ ] Native build workflow creates .app with helper paths/purpose strings and self-tests. Source-static bundle checks on Windows.
- [ ] Document native validation blocker, preserve updater guardrails and practical first-run flow.
4. Review and preserve
- [ ] Independent review, regressions for material fixes, full tests/build.
- [ ] Commit clean scoped source; publish source/verified Windows only if applicable, never untested Mac .app as ready.
