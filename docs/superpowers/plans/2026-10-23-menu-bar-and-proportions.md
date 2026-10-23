# Mac menu bar and proportional video

**Goal:** Open mac-lan as a menu-bar `.app`, preserve working LAN sharing and
automatic updates, and display the complete Mac desktop without distortion.

**Architecture:** Keep the Go LAN host and existing AppKit menu. A Go application
entry point prepares the AppKit menu from verified embedded Swift using the
Mac's existing tools, then supervises it without Terminal. The old CLI update
package installs the same app in the user's Applications directory on relaunch.
The signed feed retains asset names understood by alpha.14; a canonical app
download and its compatibility alias contain identical bytes. Capture and viewer
both preserve proportions, with non-interactive margins where screens differ.

**Constraints:** Same public repository and pinned signing key; no credentials
in artifacts; no paid CI or tool installation; no computer use. Keep 60 fps as
a target, normal macOS permission prompts, source/package provenance, legacy
configuration and consent. Never overwrite an unrelated app or user file.

- [x] Add failing capture/viewer regressions, then correct proportional sizing,
  presentation and pointer mapping, including legacy stream metadata.
- [x] Complete existing AppKit menu and native updater for an explicitly supplied
  bundle root and supervising launcher PID. Keep approval dialogs and menu actions.
- [x] Add failing Go tests for cached menu preparation and safe CLI-to-app
  installation; implement the launcher, bounded builder and app migration.
- [x] Add app-bundle validation, signed manifest bindings, compatibility selection
  and isolated apply/rollback tests while retaining alpha.14 feed compatibility.
- [ ] Package a real `.app`, bridge CLI and Windows EXE from the same clean
  commit; check identities, source hashes, credentials and legal resources.
- [ ] Update launch/release documentation, run meaningful headless checks,
  publish alpha.15 and signed feed, then verify actual anonymous downloads and
  isolated update/migration. Report native AppKit execution as unverified here.

Native Mac SSH is unavailable (connection refused). First-launch compilation is
the existing distribution mechanism, extended to the menu; it is not a claim of
native compilation or UI testing on this Windows development computer.
