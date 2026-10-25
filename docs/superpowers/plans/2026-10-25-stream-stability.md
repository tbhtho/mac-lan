# Stream stability implementation plan

**Goal:** Keep mac-lan responsive through application startup load and restore video after decoder stalls.

**Architecture:** Preserve the authenticated session and native control watchdog. Repair the existing keyframe recovery path and bound audio work before it enters the WPF dispatcher. Reject unsupported mouse buttons in the viewer before they can violate the host protocol.

**Evidence:** Current source filters `keyframe` out of `Session.Send`, schedules an unbounded dispatcher callback per audio packet, and forwards browser buttons 3/4 although the host permits only 0/1/2. The handoff supplies historical transport evidence; the latest failure has no current local log.

**Constraints:** Headless validation only; preserve FluxFrame WIP, releases, settings, permissions, credentials and updater feed. No automatic reconnect or weakened input validation. No native execution claims from Windows checks.

## Tasks

- [x] Reproduce the missing keyframe on a real synthetic TLS session, then permit requests only for ready hosts advertising support. Verify older hosts and pre-ready sessions receive none.
- [x] Reproduce unsupported pointer buttons in the viewer harness, then suppress them while preserving ordinary buttons and activation. Review also caught mixed chords; regressions cover cancellation across decoder recovery.
- [x] Add a bounded audio dispatcher queue with packet/byte/age limits and one pending callback. Exercise stalled scheduling, stale audio, overload, concurrent arrivals and rescheduling; integrate it at the existing audio dispatch boundary.
- [x] Run focused regressions, then `./scripts/test.ps1`. Review diff and documentation links. Document current-source behavior and limitations without advertising an unbuilt release. Separate Windows candidate packaged and headless-smoked; no release published.

## Acceptance limits

The tests establish recovery requests, protocol-safe input and bounded pending audio under synthetic load. FluxFrame startup on both computers, Wi-Fi interruptions, native capture and measured responsiveness still require the user's hardware run.

## Authorized follow-up: automatic control on MacBook

The user explicitly requested control as soon as the MacBook tab is selected.
This supersedes the previous full-desktop click-activation requirement. The
Windows gate records tab intent and arms when the approved stream, confirmed
Accessibility grant, fresh picture/native heartbeat and foreground focus allow
it. Manual Stop, tab exit, disconnect, shutdown and hook acquisition failure
cancel intent; transient focus/video/watchdog loss releases held input and waits
for prerequisites to recover. App Priority keeps its existing click activation.

- [x] Add a failing gate regression for tab activation without a desktop click.
- [x] Implement automatic intent and test permission/readiness/focus, recovery,
  manual Stop, tab exit, disconnect and failed hook acquisition.
- [x] Wire the desktop tab, activation, presented frames and control status.
- [x] Complete full headless checks, review, new separate candidate and smoke.
  The final combined MacBook/monitor candidate and exact hashes are recorded in
  `docs/verification.md`; no native desktop acceptance is claimed.
